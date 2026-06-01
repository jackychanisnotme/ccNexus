package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	retryReasonSemanticEmptyResponse = "semantic_empty_response"

	emptyKindResponsesEmpty = "responses_empty"
	emptyKindChatEmpty      = "chat_empty"
	emptyKindClaudeEmpty    = "claude_empty"
	emptyKindReasoningOnly  = "reasoning_only"
)

type semanticEmptyResponseError struct {
	Kind          string
	OutputTokens  int
	OutputTextLen int
}

func (e *semanticEmptyResponseError) Error() string {
	if e == nil {
		return retryReasonSemanticEmptyResponse
	}
	return fmt.Sprintf("%s empty_kind=%s output_tokens=%d outputTextLen=%d", retryReasonSemanticEmptyResponse, e.Kind, e.OutputTokens, e.OutputTextLen)
}

func newSemanticEmptyResponseError(kind string, outputTokens, outputTextLen int) *semanticEmptyResponseError {
	if strings.TrimSpace(kind) == "" {
		kind = emptyKindResponsesEmpty
	}
	return &semanticEmptyResponseError{
		Kind:          kind,
		OutputTokens:  outputTokens,
		OutputTextLen: outputTextLen,
	}
}

func hasSuccessfulOutputTokens(outputTokens int) bool {
	return outputTokens > 0
}

func asSemanticEmptyResponseError(err error) (*semanticEmptyResponseError, bool) {
	var target *semanticEmptyResponseError
	if errors.As(err, &target) && target != nil {
		return target, true
	}
	return nil, false
}

type semanticResponseInspection struct {
	Recognized    bool
	HasOutput     bool
	EmptyKind     string
	OutputTextLen int
}

type semanticStreamInspection struct {
	HasOutput   bool
	HasProgress bool
	Completed   bool
	EmptyKind   string
}

func inspectSemanticResponse(body []byte) semanticResponseInspection {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return semanticResponseInspection{}
	}

	if response, ok := payload["response"].(map[string]interface{}); ok {
		if eventType, _ := payload["type"].(string); eventType == "response.completed" || hasResponsesShape(response) {
			return inspectOpenAIResponsesPayload(response)
		}
	}
	if hasResponsesShape(payload) {
		return inspectOpenAIResponsesPayload(payload)
	}
	if _, ok := payload["choices"]; ok {
		return inspectOpenAIChatPayload(payload)
	}
	if hasClaudeShape(payload) {
		return inspectClaudePayload(payload)
	}

	return semanticResponseInspection{}
}

func semanticEmptyErrorForResponse(body []byte, outputTokens int) *semanticEmptyResponseError {
	if hasSuccessfulOutputTokens(outputTokens) {
		return nil
	}
	inspection := inspectSemanticResponse(body)
	if !inspection.Recognized || inspection.HasOutput {
		return nil
	}
	return newSemanticEmptyResponseError(inspection.EmptyKind, outputTokens, inspection.OutputTextLen)
}

func inspectSemanticStreamEvent(eventData []byte) semanticStreamInspection {
	var result semanticStreamInspection
	scanner := bufio.NewScanner(bytes.NewReader(eventData))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		jsonData := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonData == "" {
			continue
		}
		if jsonData == "[DONE]" {
			result.Completed = true
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
			continue
		}
		mergeStreamInspection(&result, inspectSemanticStreamJSON(event))
	}
	return result
}

func inspectSemanticStreamJSON(event map[string]interface{}) semanticStreamInspection {
	var result semanticStreamInspection
	if event == nil {
		return result
	}

	eventType, _ := event["type"].(string)
	// Reasoning/thinking is real progress for keep-alive but is not semantic
	// output, so empty-response detection and silent retry stay unchanged.
	switch eventType {
	case "response.reasoning_text.delta", "response.reasoning_text.done",
		"response.reasoning_summary_text.delta", "response.reasoning_summary_text.done":
		result.HasProgress = true
	}
	if item, ok := event["item"].(map[string]interface{}); ok {
		if itemType, _ := item["type"].(string); itemType == "reasoning" {
			result.HasProgress = true
		}
	}
	switch eventType {
	case "response.output_text.delta":
		if hasNonEmptyString(event["delta"]) || hasNonEmptyString(event["text"]) {
			result.HasOutput = true
		}
	case "response.output_text.done":
		if hasNonEmptyString(event["text"]) || hasNonEmptyString(event["delta"]) {
			result.HasOutput = true
		}
	case "response.content_part.added":
		if part, ok := event["part"].(map[string]interface{}); ok && hasTextInOpenAI2ContentPart(part) {
			result.HasOutput = true
		}
	case "response.content_part.done":
		if part, ok := event["part"].(map[string]interface{}); ok && hasTextInOpenAI2ContentPart(part) {
			result.HasOutput = true
		}
	case "response.output_item.added", "response.output_item.done":
		if item, ok := event["item"].(map[string]interface{}); ok && hasValidResponsesOutputItem(item) {
			result.HasOutput = true
		}
	case "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done":
		if hasNonEmptyString(event["delta"]) || hasNonEmptyString(event["input"]) {
			result.HasOutput = true
		}
	case "response.completed":
		result.Completed = true
		if response, ok := event["response"].(map[string]interface{}); ok {
			inspection := inspectOpenAIResponsesPayload(response)
			result.HasOutput = inspection.HasOutput
			result.EmptyKind = inspection.EmptyKind
		}
	case "content_block_start":
		if block, ok := event["content_block"].(map[string]interface{}); ok {
			if hasValidClaudeContentBlock(block) {
				result.HasOutput = true
			}
		}
	case "content_block_delta":
		if delta, ok := event["delta"].(map[string]interface{}); ok {
			if hasNonEmptyString(delta["text"]) || hasNonEmptyString(delta["partial_json"]) {
				result.HasOutput = true
			}
		}
	case "message_stop":
		result.Completed = true
	}

	if choices, ok := event["choices"].([]interface{}); ok {
		if len(choices) == 0 {
			result.EmptyKind = emptyKindChatEmpty
		}
		for _, rawChoice := range choices {
			choice, ok := rawChoice.(map[string]interface{})
			if !ok {
				continue
			}
			if message, ok := choice["message"].(map[string]interface{}); ok && hasValidOpenAIChatMessage(message) {
				result.HasOutput = true
			}
			if delta, ok := choice["delta"].(map[string]interface{}); ok && hasValidOpenAIChatMessage(delta) {
				result.HasOutput = true
			}
		}
	}

	return result
}

func mergeStreamInspection(dst *semanticStreamInspection, src semanticStreamInspection) {
	if src.HasOutput {
		dst.HasOutput = true
	}
	if src.HasProgress {
		dst.HasProgress = true
	}
	if src.Completed {
		dst.Completed = true
	}
	if src.EmptyKind != "" {
		dst.EmptyKind = src.EmptyKind
	}
}

func hasResponsesShape(payload map[string]interface{}) bool {
	if payload == nil {
		return false
	}
	if _, ok := payload["output"]; ok {
		return true
	}
	if object, _ := payload["object"].(string); object == "response" {
		return true
	}
	if _, ok := payload["status"].(string); ok {
		if _, hasUsage := payload["usage"]; hasUsage {
			return true
		}
	}
	return false
}

func inspectOpenAIResponsesPayload(payload map[string]interface{}) semanticResponseInspection {
	inspection := semanticResponseInspection{Recognized: true, EmptyKind: emptyKindResponsesEmpty}
	output, ok := payload["output"].([]interface{})
	if !ok || len(output) == 0 {
		return inspection
	}

	onlyReasoning := false
	for _, rawItem := range output {
		item, ok := rawItem.(map[string]interface{})
		if !ok {
			continue
		}
		itemType := strings.ToLower(strings.TrimSpace(stringFromInterface(item["type"])))
		switch itemType {
		case "message":
			if hasTextInOpenAI2Content(item["content"]) || hasValidToolCalls(item["tool_calls"]) {
				inspection.HasOutput = true
			}
		case "function_call", "custom_tool_call", "local_shell_call", "computer_call",
			"file_search_call", "web_search_call", "mcp_call", "code_interpreter_call",
			"image_generation_call":
			if hasValidResponsesToolLikeOutputItem(item) {
				inspection.HasOutput = true
			}
		case "reasoning":
			if hasReasoningContent(item["summary"]) || hasReasoningContent(item["content"]) {
				onlyReasoning = true
			}
		default:
			if strings.HasSuffix(itemType, "_call") && hasValidResponsesToolLikeOutputItem(item) {
				inspection.HasOutput = true
			} else if hasTextInOpenAI2Content(item["content"]) {
				inspection.HasOutput = true
			}
		}
		if inspection.HasOutput {
			break
		}
	}
	if !inspection.HasOutput && onlyReasoning {
		inspection.EmptyKind = emptyKindReasoningOnly
	}
	inspection.OutputTextLen = len(extractResponseOutputText(mustMarshalJSON(payload)))
	return inspection
}

func inspectOpenAIChatPayload(payload map[string]interface{}) semanticResponseInspection {
	inspection := semanticResponseInspection{Recognized: true, EmptyKind: emptyKindChatEmpty}
	choices, ok := payload["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return inspection
	}

	onlyReasoning := false
	for _, rawChoice := range choices {
		choice, ok := rawChoice.(map[string]interface{})
		if !ok {
			continue
		}
		message, _ := choice["message"].(map[string]interface{})
		if message == nil {
			message, _ = choice["delta"].(map[string]interface{})
		}
		if message == nil {
			continue
		}
		if hasValidOpenAIChatMessage(message) {
			inspection.HasOutput = true
			break
		}
		if hasReasoningContent(message["reasoning_content"]) {
			onlyReasoning = true
		}
	}
	if !inspection.HasOutput && onlyReasoning {
		inspection.EmptyKind = emptyKindReasoningOnly
	}
	inspection.OutputTextLen = len(extractResponseOutputText(mustMarshalJSON(payload)))
	return inspection
}

func hasClaudeShape(payload map[string]interface{}) bool {
	if payload == nil {
		return false
	}
	if _, ok := payload["content"]; ok {
		return true
	}
	if payloadType, _ := payload["type"].(string); payloadType == "message" {
		return true
	}
	return false
}

func inspectClaudePayload(payload map[string]interface{}) semanticResponseInspection {
	inspection := semanticResponseInspection{Recognized: true, EmptyKind: emptyKindClaudeEmpty}
	if hasNonEmptyString(payload["content"]) {
		inspection.HasOutput = true
		inspection.OutputTextLen = len(stringFromInterface(payload["content"]))
		return inspection
	}

	content, ok := payload["content"].([]interface{})
	if !ok || len(content) == 0 {
		return inspection
	}

	onlyReasoning := false
	for _, rawBlock := range content {
		block, ok := rawBlock.(map[string]interface{})
		if !ok {
			continue
		}
		blockType := strings.ToLower(strings.TrimSpace(stringFromInterface(block["type"])))
		switch blockType {
		case "text":
			if hasNonEmptyString(block["text"]) {
				inspection.HasOutput = true
			}
		case "tool_use":
			if hasValidClaudeToolUse(block) {
				inspection.HasOutput = true
			}
		case "thinking", "reasoning":
			if hasReasoningContent(block["thinking"]) || hasReasoningContent(block["text"]) {
				onlyReasoning = true
			}
		}
		if inspection.HasOutput {
			break
		}
	}
	if !inspection.HasOutput && onlyReasoning {
		inspection.EmptyKind = emptyKindReasoningOnly
	}
	inspection.OutputTextLen = len(extractResponseOutputText(mustMarshalJSON(payload)))
	return inspection
}

func hasValidOpenAIChatMessage(message map[string]interface{}) bool {
	if message == nil {
		return false
	}
	if hasNonEmptyString(message["content"]) || hasTextInOpenAI2Content(message["content"]) {
		return true
	}
	if hasNonEmptyString(message["refusal"]) {
		return true
	}
	return hasValidToolCalls(message["tool_calls"])
}

func hasValidToolCalls(value interface{}) bool {
	toolCalls, ok := value.([]interface{})
	if !ok || len(toolCalls) == 0 {
		return false
	}
	for _, rawToolCall := range toolCalls {
		toolCall, ok := rawToolCall.(map[string]interface{})
		if !ok {
			continue
		}
		if hasNonEmptyString(toolCall["id"]) || hasNonEmptyString(toolCall["call_id"]) {
			return true
		}
		if function, ok := toolCall["function"].(map[string]interface{}); ok {
			if hasNonEmptyString(function["name"]) {
				return true
			}
		}
	}
	return false
}

func hasValidResponsesOutputItem(item map[string]interface{}) bool {
	if item == nil {
		return false
	}
	itemType := strings.ToLower(strings.TrimSpace(stringFromInterface(item["type"])))
	switch itemType {
	case "message":
		return hasTextInOpenAI2Content(item["content"]) || hasValidToolCalls(item["tool_calls"])
	case "function_call", "custom_tool_call", "local_shell_call", "computer_call",
		"file_search_call", "web_search_call", "mcp_call", "code_interpreter_call",
		"image_generation_call":
		return hasValidResponsesToolLikeOutputItem(item)
	default:
		if strings.HasSuffix(itemType, "_call") {
			return hasValidResponsesToolLikeOutputItem(item)
		}
		return hasTextInOpenAI2Content(item["content"])
	}
}

func hasValidResponsesFunctionCall(item map[string]interface{}) bool {
	return hasNonEmptyString(item["name"]) ||
		hasNonEmptyString(item["call_id"]) ||
		hasNonEmptyString(item["id"]) ||
		hasNonEmptyString(item["arguments"])
}

func hasValidResponsesToolLikeOutputItem(item map[string]interface{}) bool {
	if hasValidResponsesFunctionCall(item) ||
		hasNonEmptyString(item["status"]) ||
		hasNonEmptyString(item["input"]) ||
		hasNonEmptyString(item["output"]) {
		return true
	}
	if action, ok := item["action"].(map[string]interface{}); ok && len(action) > 0 {
		return true
	}
	if results, ok := item["results"].([]interface{}); ok && len(results) > 0 {
		return true
	}
	return false
}

func hasValidClaudeContentBlock(block map[string]interface{}) bool {
	if block == nil {
		return false
	}
	blockType := strings.ToLower(strings.TrimSpace(stringFromInterface(block["type"])))
	if blockType == "tool_use" {
		return hasValidClaudeToolUse(block)
	}
	return blockType == "text" && hasNonEmptyString(block["text"])
}

func hasValidClaudeToolUse(block map[string]interface{}) bool {
	return hasNonEmptyString(block["id"]) || hasNonEmptyString(block["name"])
}

func hasTextInOpenAI2Content(content interface{}) bool {
	switch value := content.(type) {
	case string:
		return strings.TrimSpace(value) != ""
	case []interface{}:
		for _, rawPart := range value {
			part, ok := rawPart.(map[string]interface{})
			if !ok {
				continue
			}
			if hasTextInOpenAI2ContentPart(part) {
				return true
			}
		}
	}
	return false
}

func hasTextInOpenAI2ContentPart(part map[string]interface{}) bool {
	if part == nil {
		return false
	}
	return hasNonEmptyString(part["text"]) || hasNonEmptyString(part["delta"])
}

func hasReasoningContent(value interface{}) bool {
	if hasNonEmptyString(value) {
		return true
	}
	switch parts := value.(type) {
	case []interface{}:
		for _, rawPart := range parts {
			part, ok := rawPart.(map[string]interface{})
			if !ok {
				continue
			}
			if hasNonEmptyString(part["text"]) || hasNonEmptyString(part["summary_text"]) {
				return true
			}
		}
	}
	return false
}

func hasNonEmptyString(value interface{}) bool {
	return strings.TrimSpace(stringFromInterface(value)) != ""
}

func stringFromInterface(value interface{}) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func mustMarshalJSON(value interface{}) []byte {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return payload
}
