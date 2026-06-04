package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lich0821/ccNexus/internal/transformer"
)

// OpenAIReqToOpenAI2 converts OpenAI Chat request to OpenAI Responses request
func OpenAIReqToOpenAI2(openaiReq []byte, model string) ([]byte, error) {
	var req transformer.OpenAIRequest
	if err := json.Unmarshal(openaiReq, &req); err != nil {
		return nil, err
	}

	openai2Req := map[string]interface{}{
		"model":  model,
		"stream": req.Stream,
	}
	if req.Reasoning != nil {
		openai2Req["reasoning"] = req.Reasoning
	} else if effort := strings.ToLower(strings.TrimSpace(req.ReasoningEffort)); effort != "" {
		openai2Req["reasoning"] = map[string]interface{}{"effort": effort}
	}

	var input []map[string]interface{}
	for _, msg := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "system" {
			if content, ok := msg.Content.(string); ok {
				openai2Req["instructions"] = content
			}
			continue
		}
		if role == "tool" {
			input = append(input, openAIToolMessageToOpenAI2Output(msg))
			continue
		}

		if role == "assistant" && strings.TrimSpace(msg.ReasoningContent) != "" {
			input = append(input, openAIReasoningToOpenAI2Item(msg.ReasoningContent))
		}
		if item, ok := openAIMessageToOpenAI2Message(msg, role); ok {
			input = append(input, item)
		}
		input = append(input, openAIToolCallsToOpenAI2Items(msg.ToolCalls)...)
	}
	openai2Req["input"] = input
	// TODO: max_output_tokens is standard OpenAI Responses API param but some
	// third-party endpoints (e.g. SiliconFlow) don't support it. Skipping for compatibility.

	if len(req.Tools) > 0 {
		var tools []map[string]interface{}
		for _, tool := range req.Tools {
			if tool.Type == "function" {
				tools = append(tools, map[string]interface{}{
					"type":        "function",
					"name":        tool.Function.Name,
					"description": tool.Function.Description,
					"parameters":  tool.Function.Parameters,
				})
			}
		}
		openai2Req["tools"] = tools

		// Preserve explicit tool routing semantics when moving to Responses API.
		if mapped := mapOpenAIToolChoiceToOpenAI2(req.ToolChoice); mapped != nil {
			openai2Req["tool_choice"] = mapped
		} else {
			// Keep explicit default for compatibility with providers that do not
			// treat omitted tool_choice as "auto".
			openai2Req["tool_choice"] = "auto"
		}
	}

	return json.Marshal(openai2Req)
}

// NormalizeOpenAI2RequestForUpstream converts Chat-style tool messages that some
// clients put into Responses input into native Responses function output items.
func NormalizeOpenAI2RequestForUpstream(openai2Req []byte) ([]byte, error) {
	var body map[string]interface{}
	if err := json.Unmarshal(openai2Req, &body); err != nil {
		return nil, err
	}

	rawInput, ok := body["input"].([]interface{})
	if !ok {
		return openai2Req, nil
	}

	normalizedInput, changed := normalizeOpenAI2InputForUpstream(rawInput)
	if !changed {
		return openai2Req, nil
	}

	body["input"] = normalizedInput
	return json.Marshal(body)
}

func normalizeOpenAI2InputForUpstream(input []interface{}) ([]interface{}, bool) {
	normalized := make([]interface{}, 0, len(input))
	changed := false

	for _, item := range input {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			normalized = append(normalized, item)
			continue
		}

		role := strings.ToLower(strings.TrimSpace(stringFromMap(itemMap, "role")))
		itemType := strings.ToLower(strings.TrimSpace(stringFromMap(itemMap, "type")))

		if role == "tool" {
			normalized = append(normalized, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": firstStringFromMap(itemMap, "call_id", "tool_call_id", "id"),
				"output":  stringifyOpenAIMessageContent(firstNonNilFromMap(itemMap, "output", "content")),
			})
			changed = true
			continue
		}

		if role == "assistant" {
			if toolCalls, ok := itemMap["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
				message := cloneMapWithoutKeys(itemMap, "tool_calls")
				if itemType == "" {
					message["type"] = "message"
				}
				if hasMeaningfulOpenAI2MessageContent(message["content"]) {
					normalized = append(normalized, message)
				}
				for _, toolCallItem := range genericToolCallsToOpenAI2Items(toolCalls) {
					normalized = append(normalized, toolCallItem)
				}
				changed = true
				continue
			}
		}

		if itemType == "" && role != "" {
			message := cloneMapWithoutKeys(itemMap)
			message["type"] = "message"
			normalized = append(normalized, message)
			changed = true
			continue
		}

		normalized = append(normalized, item)
	}

	return normalized, changed
}

func openAIMessageToOpenAI2Message(msg transformer.OpenAIMessage, role string) (map[string]interface{}, bool) {
	item := map[string]interface{}{"type": "message", "role": role}
	contentParts := openAIContentToOpenAI2Parts(msg.Content, role)
	if len(msg.ToolCalls) > 0 && !hasMeaningfulOpenAI2ContentParts(contentParts) {
		return nil, false
	}
	item["content"] = contentParts
	return item, true
}

func openAIContentToOpenAI2Parts(content interface{}, role string) []map[string]interface{} {
	textType := "input_text"
	if role == "assistant" {
		textType = "output_text"
	}

	switch value := content.(type) {
	case string:
		return []map[string]interface{}{{"type": textType, "text": value}}
	case []interface{}:
		parts := make([]map[string]interface{}, 0, len(value))
		for _, rawPart := range value {
			partMap, ok := rawPart.(map[string]interface{})
			if !ok {
				continue
			}
			partType, _ := partMap["type"].(string)
			text, _ := partMap["text"].(string)
			switch partType {
			case "text", "input_text", "output_text":
				parts = append(parts, map[string]interface{}{"type": textType, "text": text})
			}
		}
		return parts
	case nil:
		return nil
	default:
		return []map[string]interface{}{{"type": textType, "text": stringifyOpenAIMessageContent(value)}}
	}
}

func openAIToolMessageToOpenAI2Output(msg transformer.OpenAIMessage) map[string]interface{} {
	return map[string]interface{}{
		"type":    "function_call_output",
		"call_id": strings.TrimSpace(msg.ToolCallID),
		"output":  stringifyOpenAIMessageContent(msg.Content),
	}
}

func openAIReasoningToOpenAI2Item(text string) map[string]interface{} {
	return map[string]interface{}{
		"type": "reasoning",
		"summary": []map[string]interface{}{
			{"type": "summary_text", "text": text},
		},
	}
}

func openAIToolCallsToOpenAI2Items(toolCalls []transformer.OpenAIToolCall) []map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		if strings.TrimSpace(toolCall.ID) == "" && strings.TrimSpace(toolCall.Function.Name) == "" {
			continue
		}
		items = append(items, map[string]interface{}{
			"type":      "function_call",
			"call_id":   strings.TrimSpace(toolCall.ID),
			"name":      strings.TrimSpace(toolCall.Function.Name),
			"arguments": toolCall.Function.Arguments,
		})
	}
	return items
}

func genericToolCallsToOpenAI2Items(toolCalls []interface{}) []interface{} {
	items := make([]interface{}, 0, len(toolCalls))
	for _, rawToolCall := range toolCalls {
		toolCall, ok := rawToolCall.(map[string]interface{})
		if !ok {
			continue
		}
		function, _ := toolCall["function"].(map[string]interface{})
		items = append(items, map[string]interface{}{
			"type":      "function_call",
			"call_id":   firstStringFromMap(toolCall, "call_id", "id"),
			"name":      stringFromMap(function, "name"),
			"arguments": stringFromMap(function, "arguments"),
		})
	}
	return items
}

func stringifyOpenAIMessageContent(content interface{}) string {
	switch value := content.(type) {
	case nil:
		return ""
	case string:
		return value
	case []interface{}:
		var text strings.Builder
		for _, rawPart := range value {
			partMap, ok := rawPart.(map[string]interface{})
			if !ok {
				continue
			}
			if partText, ok := partMap["text"].(string); ok {
				text.WriteString(partText)
			}
		}
		if text.Len() > 0 {
			return text.String()
		}
	}

	data, err := json.Marshal(content)
	if err != nil {
		return fmt.Sprint(content)
	}
	return string(data)
}

func extractOpenAI2ReasoningTextFromMap(item map[string]interface{}) string {
	if item == nil {
		return ""
	}
	if text := strings.TrimSpace(stringFromMap(item, "text")); text != "" {
		return text
	}
	if text := extractOpenAI2Text(item["summary"]); text != "" {
		return text
	}
	if text := extractOpenAI2Text(item["content"]); text != "" {
		return text
	}
	return ""
}

func hasMeaningfulOpenAI2MessageContent(content interface{}) bool {
	switch value := content.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(value) != ""
	case []interface{}:
		for _, rawPart := range value {
			partMap, ok := rawPart.(map[string]interface{})
			if !ok {
				return true
			}
			if strings.TrimSpace(stringFromMap(partMap, "text")) != "" {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func hasMeaningfulOpenAI2ContentParts(parts []map[string]interface{}) bool {
	for _, part := range parts {
		if strings.TrimSpace(stringFromMap(part, "text")) != "" {
			return true
		}
	}
	return false
}

func cloneMapWithoutKeys(src map[string]interface{}, keys ...string) map[string]interface{} {
	dst := make(map[string]interface{}, len(src))
	skip := make(map[string]bool, len(keys))
	for _, key := range keys {
		skip[key] = true
	}
	for key, value := range src {
		if skip[key] {
			continue
		}
		dst[key] = value
	}
	return dst
}

func firstNonNilFromMap(values map[string]interface{}, keys ...string) interface{} {
	for _, key := range keys {
		if value, ok := values[key]; ok && value != nil {
			return value
		}
	}
	return nil
}

func firstStringFromMap(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringFromMap(values, key)); value != "" {
			return value
		}
	}
	return ""
}

func stringFromMap(values map[string]interface{}, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

// OpenAI2ReqToOpenAI converts OpenAI Responses request to OpenAI Chat request
func OpenAI2ReqToOpenAI(openai2Req []byte, model string) ([]byte, error) {
	var req transformer.OpenAI2Request
	if err := json.Unmarshal(openai2Req, &req); err != nil {
		return nil, err
	}

	messages := make([]transformer.OpenAIMessage, 0)
	var pendingReasoningContent string

	if req.Instructions != "" {
		messages = append(messages, transformer.OpenAIMessage{Role: "system", Content: req.Instructions})
	}

	switch v := req.Input.(type) {
	case string:
		messages = append(messages, transformer.OpenAIMessage{Role: "user", Content: v})
	case []interface{}:
		var pendingToolCalls []transformer.OpenAIToolCall

		for _, item := range v {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			itemType, _ := itemMap["type"].(string)
			switch itemType {
			case "reasoning":
				pendingReasoningContent += extractOpenAI2ReasoningTextFromMap(itemMap)

			case "message":
				// Flush pending tool calls
				if len(pendingToolCalls) > 0 {
					messages = append(messages, transformer.OpenAIMessage{Role: "assistant", ToolCalls: pendingToolCalls})
					pendingToolCalls = nil
				}
				role, _ := itemMap["role"].(string)
				if role == "developer" {
					role = "system"
				}
				text := extractOpenAI2Text(itemMap["content"])
				msg := transformer.OpenAIMessage{Role: role, Content: text}
				if role == "assistant" && pendingReasoningContent != "" {
					msg.ReasoningContent = pendingReasoningContent
					pendingReasoningContent = ""
				}
				messages = append(messages, msg)

			case "function_call":
				callID, _ := itemMap["call_id"].(string)
				name, _ := itemMap["name"].(string)
				args, _ := itemMap["arguments"].(string)
				pendingToolCalls = append(pendingToolCalls, transformer.OpenAIToolCall{
					ID:   callID,
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: name, Arguments: args},
				})

			case "function_call_output":
				// Flush pending tool calls first
				if len(pendingToolCalls) > 0 {
					messages = append(messages, transformer.OpenAIMessage{Role: "assistant", ToolCalls: pendingToolCalls})
					pendingToolCalls = nil
				}
				callID, _ := itemMap["call_id"].(string)
				output, _ := itemMap["output"].(string)
				messages = append(messages, transformer.OpenAIMessage{Role: "tool", Content: output, ToolCallID: callID})
			}
		}

		// Flush remaining
		if len(pendingToolCalls) > 0 {
			messages = append(messages, transformer.OpenAIMessage{Role: "assistant", ToolCalls: pendingToolCalls})
		}
	}
	if pendingReasoningContent != "" {
		messages = append(messages, transformer.OpenAIMessage{Role: "assistant", Content: "", ReasoningContent: pendingReasoningContent})
	}

	openaiReq := transformer.OpenAIRequest{
		Model:    model,
		Messages: messages,
		Stream:   req.Stream,
	}
	if req.Reasoning != nil {
		if effort, ok := req.Reasoning["effort"].(string); ok {
			openaiReq.ReasoningEffort = strings.ToLower(strings.TrimSpace(effort))
		}
	}

	if req.MaxOutputTokens > 0 {
		openaiReq.MaxCompletionTokens = req.MaxOutputTokens
	}

	if len(req.Tools) > 0 {
		for _, tool := range req.Tools {
			var params map[string]interface{}
			switch tool.Type {
			case "function":
				params = tool.Parameters
			case "custom":
				// Custom tools (like apply_patch) use format instead of parameters
				// Convert to a function that accepts a single string input
				params = map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"input": map[string]interface{}{"type": "string", "description": "The input for this tool"},
					},
					"required": []string{"input"},
				}
			default:
				continue
			}
			openaiReq.Tools = append(openaiReq.Tools, transformer.OpenAITool{
				Type: "function",
				Function: struct {
					Name        string                 `json:"name"`
					Description string                 `json:"description,omitempty"`
					Parameters  map[string]interface{} `json:"parameters"`
				}{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  params,
				},
			})
		}
	}

	if req.ToolChoice != nil {
		openaiReq.ToolChoice = mapOpenAI2ToolChoiceToOpenAI(req.ToolChoice)
	}

	return json.Marshal(openaiReq)
}

func mapOpenAIToolChoiceToOpenAI2(toolChoice interface{}) interface{} {
	if toolChoice == nil {
		return nil
	}

	switch tc := toolChoice.(type) {
	case string:
		return tc
	case map[string]interface{}:
		choiceType, _ := tc["type"].(string)
		if choiceType != "function" {
			return nil
		}

		// Chat Completions shape: {"type":"function","function":{"name":"..."}}
		if fn, ok := tc["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok && name != "" {
				return map[string]interface{}{"type": "function", "name": name}
			}
		}

		// Responses-compatible shape already.
		if name, ok := tc["name"].(string); ok && name != "" {
			return map[string]interface{}{"type": "function", "name": name}
		}
	}

	return nil
}

func mapOpenAI2ToolChoiceToOpenAI(toolChoice interface{}) interface{} {
	if toolChoice == nil {
		return nil
	}

	switch tc := toolChoice.(type) {
	case string:
		return tc
	case map[string]interface{}:
		choiceType, _ := tc["type"].(string)
		if choiceType == "function" {
			if name, ok := tc["name"].(string); ok && name != "" {
				return map[string]interface{}{
					"type": "function",
					"function": map[string]string{
						"name": name,
					},
				}
			}
		}
	}

	return nil
}

// OpenAIRespToOpenAI2 converts OpenAI Chat response to OpenAI Responses response
func OpenAIRespToOpenAI2(openaiResp []byte) ([]byte, error) {
	var resp transformer.OpenAIResponse
	if err := json.Unmarshal(openaiResp, &resp); err != nil {
		return nil, err
	}

	var output []map[string]interface{}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		if strings.TrimSpace(choice.Message.ReasoningContent) != "" {
			output = append(output, openAIReasoningToOpenAI2Item(choice.Message.ReasoningContent))
		}
		if choice.Message.Content != "" {
			output = append(output, map[string]interface{}{
				"type": "message",
				"role": "assistant",
				"content": []map[string]interface{}{
					{"type": "output_text", "text": choice.Message.Content},
				},
			})
		}
		for _, tc := range choice.Message.ToolCalls {
			output = append(output, map[string]interface{}{
				"type":      "function_call",
				"call_id":   tc.ID,
				"name":      tc.Function.Name,
				"arguments": tc.Function.Arguments,
			})
		}
	}

	openai2Resp := map[string]interface{}{
		"id":     resp.ID,
		"object": "response",
		"status": "completed",
		"output": output,
		"usage": map[string]interface{}{
			"input_tokens":  resp.Usage.PromptTokens,
			"output_tokens": resp.Usage.CompletionTokens,
			"total_tokens":  resp.Usage.TotalTokens,
		},
	}

	return json.Marshal(openai2Resp)
}

func extractOpenAI2ReasoningText(item transformer.OpenAI2OutputItem) string {
	var text strings.Builder
	for _, part := range item.Summary {
		if part.Text != "" {
			text.WriteString(part.Text)
		}
	}
	for _, part := range item.Content {
		if part.Text != "" {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}

// OpenAI2RespToOpenAI converts OpenAI Responses response to OpenAI Chat response
func OpenAI2RespToOpenAI(openai2Resp []byte, model string) ([]byte, error) {
	var resp transformer.OpenAI2Response
	if err := json.Unmarshal(openai2Resp, &resp); err != nil {
		return nil, err
	}

	var textContent string
	var reasoningContent string
	var toolCalls []map[string]interface{}

	for _, item := range resp.Output {
		switch item.Type {
		case "reasoning":
			reasoningContent += extractOpenAI2ReasoningText(item)
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" {
					textContent += part.Text
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, map[string]interface{}{
				"id":   item.CallID,
				"type": "function",
				"function": map[string]interface{}{
					"name":      item.Name,
					"arguments": item.Arguments,
				},
			})
		}
	}

	message := map[string]interface{}{"role": "assistant", "content": textContent}
	if reasoningContent != "" {
		message["reasoning_content"] = reasoningContent
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	openaiResp := map[string]interface{}{
		"id":      resp.ID,
		"object":  "chat.completion",
		"model":   model,
		"choices": []map[string]interface{}{{"index": 0, "message": message, "finish_reason": finishReason}},
		"usage": map[string]interface{}{
			"prompt_tokens":     resp.Usage.InputTokens,
			"completion_tokens": resp.Usage.OutputTokens,
			"total_tokens":      resp.Usage.TotalTokens,
		},
	}
	if resp.Usage.TotalTokens == 0 {
		openaiResp["usage"].(map[string]interface{})["total_tokens"] = resp.Usage.InputTokens + resp.Usage.OutputTokens
	}

	return json.Marshal(openaiResp)
}

// OpenAIStreamToOpenAI2 converts OpenAI Chat stream chunk to OpenAI Responses stream event
func OpenAIStreamToOpenAI2(event []byte, ctx *transformer.StreamContext) ([]byte, error) {
	_, jsonData := parseSSE(event)
	if jsonData == "" || jsonData == "[DONE]" {
		if jsonData == "[DONE]" && !ctx.FinishReasonSent {
			// Handle [DONE] if finish_reason wasn't received
			var result strings.Builder
			writeEvent := func(evt map[string]interface{}) {
				writeOpenAI2StreamEvent(ctx, &result, evt)
			}
			if ctx.ReasoningOutputStarted && !ctx.ReasoningOutputDone {
				writeEvent(map[string]interface{}{
					"type":          "response.reasoning_text.done",
					"output_index":  ctx.ReasoningOutputIndex,
					"content_index": 0,
					"item_id":       openAI2OutputItemID(ctx, ctx.ReasoningOutputIndex),
					"text":          ctx.ReasoningText,
				})
				writeEvent(map[string]interface{}{
					"type":         "response.output_item.done",
					"output_index": ctx.ReasoningOutputIndex,
					"item":         openAI2ReasoningItem(ctx, ctx.ReasoningOutputIndex, "completed"),
				})
				ctx.ReasoningOutputDone = true
			}
			if ctx.ContentBlockStarted {
				outputIndex := responseMessageOutputIndex(ctx)
				writeEvent(openAI2TextDoneEvent(ctx, outputIndex))
				writeEvent(openAI2ContentPartDoneEvent(ctx, outputIndex))
				writeEvent(map[string]interface{}{"type": "response.output_item.done", "output_index": outputIndex, "item": openAI2MessageItem(ctx, outputIndex, "completed")})
			}
			writeEvent(openAI2CompletedEvent(ctx, 0))
			result.WriteString("data: [DONE]\n\n")
			return []byte(result.String()), nil
		}
		return nil, nil
	}

	// Check for error response
	var errResp struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(jsonData), &errResp); err == nil && errResp.Error.Message != "" {
		return nil, fmt.Errorf("upstream error: %s", errResp.Error.Message)
	}

	var chunk transformer.OpenAIStreamChunk
	if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
		return nil, nil
	}
	if chunk.Usage != nil {
		if chunk.Usage.PromptTokens > 0 {
			ctx.InputTokens = chunk.Usage.PromptTokens
		}
		if chunk.Usage.CompletionTokens > 0 {
			ctx.OutputTokens = chunk.Usage.CompletionTokens
		}
	}

	var result strings.Builder
	writeEvent := func(evt map[string]interface{}) {
		writeOpenAI2StreamEvent(ctx, &result, evt)
	}
	writeReasoningDone := func() {
		if !ctx.ReasoningOutputStarted || ctx.ReasoningOutputDone {
			return
		}
		writeEvent(map[string]interface{}{
			"type":          "response.reasoning_text.done",
			"output_index":  ctx.ReasoningOutputIndex,
			"content_index": 0,
			"item_id":       openAI2OutputItemID(ctx, ctx.ReasoningOutputIndex),
			"text":          ctx.ReasoningText,
		})
		writeEvent(map[string]interface{}{
			"type":         "response.output_item.done",
			"output_index": ctx.ReasoningOutputIndex,
			"item":         openAI2ReasoningItem(ctx, ctx.ReasoningOutputIndex, "completed"),
		})
		ctx.ReasoningOutputDone = true
	}

	if !ctx.MessageStartSent {
		ctx.MessageStartSent = true
		ctx.MessageID = chunk.ID
		writeEvent(openAI2CreatedEvent(ctx))
	}

	if len(chunk.Choices) > 0 {
		delta := chunk.Choices[0].Delta
		finishReason := chunk.Choices[0].FinishReason

		// Handle reasoning content before text content.
		if delta.ReasoningContent != "" {
			if !ctx.ReasoningOutputStarted {
				ctx.ReasoningOutputStarted = true
				if ctx.ContentBlockStarted || ctx.ToolBlockStarted || ctx.ToolCallCounter > 0 {
					ctx.ReasoningOutputIndex = 1
				} else {
					ctx.ReasoningOutputIndex = 0
				}
				writeEvent(map[string]interface{}{
					"type":         "response.output_item.added",
					"output_index": ctx.ReasoningOutputIndex,
					"item":         openAI2ReasoningItem(ctx, ctx.ReasoningOutputIndex, "in_progress"),
				})
			}
			ctx.ReasoningText += delta.ReasoningContent
			recordOpenAI2Reasoning(ctx, ctx.ReasoningOutputIndex, delta.ReasoningContent)
			writeEvent(map[string]interface{}{
				"type":          "response.reasoning_text.delta",
				"output_index":  ctx.ReasoningOutputIndex,
				"content_index": 0,
				"item_id":       openAI2OutputItemID(ctx, ctx.ReasoningOutputIndex),
				"delta":         delta.ReasoningContent,
			})
		}

		// Handle text content
		if delta.Content != "" {
			if !ctx.ContentBlockStarted {
				ctx.ContentBlockStarted = true
				outputIndex := responseMessageOutputIndex(ctx)
				writeEvent(map[string]interface{}{
					"type": "response.output_item.added", "output_index": outputIndex,
					"item": openAI2MessageItem(ctx, outputIndex, "in_progress"),
				})
				writeEvent(openAI2ContentPartAddedEvent(ctx, outputIndex))
			}
			writeEvent(openAI2TextDeltaEvent(ctx, responseMessageOutputIndex(ctx), delta.Content))
		}

		// Handle tool calls
		for _, tc := range delta.ToolCalls {
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			// New tool call (has ID)
			if tc.ID != "" {
				ctx.ToolCallCounter++
				ctx.CurrentToolID = tc.ID
				ctx.CurrentToolName = tc.Function.Name
				ctx.ToolArguments = ""
				outputIndex := responseToolOutputIndex(ctx, idx)
				recordOpenAI2ToolCall(ctx, outputIndex, tc.ID, tc.Function.Name)
				writeEvent(map[string]interface{}{
					"type": "response.output_item.added", "output_index": outputIndex,
					"item": openAI2ToolItem(ctx, outputIndex, "in_progress"),
				})
			}
			// Accumulate arguments
			if tc.Function.Arguments != "" {
				ctx.ToolArguments += tc.Function.Arguments
				outputIndex := responseToolOutputIndex(ctx, idx)
				recordOpenAI2ToolArguments(ctx, outputIndex, tc.Function.Arguments)
				writeEvent(map[string]interface{}{
					"type":         "response.function_call_arguments.delta",
					"output_index": outputIndex,
					"item_id":      openAI2OutputItemID(ctx, outputIndex),
					"delta":        tc.Function.Arguments,
				})
			}
		}

		// Handle finish
		if finishReason != nil && *finishReason != "" {
			writeReasoningDone()
			if ctx.ContentBlockStarted {
				outputIndex := responseMessageOutputIndex(ctx)
				writeEvent(openAI2TextDoneEvent(ctx, outputIndex))
				writeEvent(openAI2ContentPartDoneEvent(ctx, outputIndex))
				writeEvent(map[string]interface{}{"type": "response.output_item.done", "output_index": outputIndex, "item": openAI2MessageItem(ctx, outputIndex, "completed")})
				ctx.ContentBlockStarted = false
			}
			if *finishReason == "tool_calls" && ctx.CurrentToolID != "" {
				outputIndex := responseToolOutputIndex(ctx, 0)
				writeEvent(map[string]interface{}{
					"type":         "response.function_call_arguments.done",
					"output_index": outputIndex,
					"item_id":      openAI2OutputItemID(ctx, outputIndex),
					"name":         ctx.CurrentToolName,
					"arguments":    ctx.ToolArguments,
				})
				writeEvent(map[string]interface{}{
					"type":         "response.output_item.done",
					"output_index": outputIndex,
					"item":         openAI2ToolItem(ctx, outputIndex, "completed"),
				})
			}
			writeEvent(openAI2CompletedEvent(ctx, 0))
			result.WriteString("data: [DONE]\n\n")
			ctx.FinishReasonSent = true
		}
	}

	if result.Len() > 0 {
		return []byte(result.String()), nil
	}
	return nil, nil
}

func responseMessageOutputIndex(ctx *transformer.StreamContext) int {
	if ctx != nil && ctx.ReasoningOutputStarted && ctx.ReasoningOutputIndex == 0 {
		return 1
	}
	return 0
}

func responseToolOutputIndex(ctx *transformer.StreamContext, toolIndex int) int {
	messageIndex := responseMessageOutputIndex(ctx)
	outputIndex := messageIndex + toolIndex + 1
	if ctx != nil && ctx.ReasoningOutputStarted && ctx.ReasoningOutputIndex > messageIndex && ctx.ReasoningOutputIndex <= outputIndex {
		outputIndex++
	}
	return outputIndex
}

func buildOpenAIReasoningChunk(id, model, reasoning string) ([]byte, error) {
	if reasoning == "" {
		return nil, nil
	}
	chunk := map[string]interface{}{
		"id": id, "object": "chat.completion.chunk", "model": model,
		"choices": []map[string]interface{}{{
			"index":         0,
			"delta":         map[string]interface{}{"reasoning_content": reasoning},
			"finish_reason": nil,
		}},
	}
	data, _ := json.Marshal(chunk)
	return []byte(fmt.Sprintf("data: %s\n\n", data)), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// OpenAI2StreamToOpenAI converts OpenAI Responses stream event to OpenAI Chat stream chunk
func OpenAI2StreamToOpenAI(event []byte, ctx *transformer.StreamContext, model string) ([]byte, error) {
	_, jsonData := parseSSE(event)
	if jsonData == "" || jsonData == "[DONE]" {
		if jsonData == "[DONE]" {
			return []byte("data: [DONE]\n\n"), nil
		}
		return nil, nil
	}

	var evt transformer.OpenAI2StreamEvent
	if err := json.Unmarshal([]byte(jsonData), &evt); err != nil {
		return nil, nil
	}

	switch evt.Type {
	case "response.created":
		if evt.Response != nil {
			ctx.MessageID = evt.Response.ID
		}
		return nil, nil

	case "response.output_text.delta":
		text := firstNonEmpty(evt.Delta, evt.Text)
		recordOpenAI2Text(ctx, evt.OutputIndex, text)
		return buildOpenAIChunk(ctx.MessageID, model, text, nil, "")

	case "response.output_text.done", "response.content_part.done":
		text := firstNonEmpty(evt.Text, evt.Delta)
		if text == "" && evt.Part != nil {
			text = evt.Part.Text
		}
		missingText := missingOpenAI2Text(ctx, evt.OutputIndex, text)
		if missingText == "" {
			return nil, nil
		}
		return buildOpenAIChunk(ctx.MessageID, model, missingText, nil, "")

	case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		return buildOpenAIReasoningChunk(ctx.MessageID, model, firstNonEmpty(evt.Delta, evt.Text))

	case "response.output_item.added":
		if evt.Item != nil && evt.Item.Type == "function_call" {
			ctx.ToolBlockStarted = true
			ctx.CurrentToolID = evt.Item.CallID
			ctx.CurrentToolName = evt.Item.Name
			ctx.ToolArguments = ""
		}
		return nil, nil

	case "response.function_call_arguments.delta":
		if ctx.ToolBlockStarted {
			ctx.ToolArguments += evt.Delta
		}
		return nil, nil

	case "response.output_item.done":
		if evt.Item != nil && evt.Item.Type == "function_call" && ctx.ToolBlockStarted {
			ctx.ToolBlockStarted = false
			return buildOpenAIChunk(ctx.MessageID, model, "", []map[string]interface{}{
				{"index": ctx.ToolIndex, "id": ctx.CurrentToolID, "type": "function",
					"function": map[string]interface{}{"name": ctx.CurrentToolName, "arguments": ctx.ToolArguments}},
			}, "")
		}
		return nil, nil

	case "response.completed":
		if evt.Response != nil {
			if ctx.MessageID == "" {
				ctx.MessageID = evt.Response.ID
			}
			if evt.Response.Usage.InputTokens > 0 {
				ctx.InputTokens = evt.Response.Usage.InputTokens
			}
			if evt.Response.Usage.OutputTokens > 0 {
				ctx.OutputTokens = evt.Response.Usage.OutputTokens
			}
		}
		finishReason := "stop"
		if ctx.CurrentToolID != "" {
			finishReason = "tool_calls"
		}
		usage := map[string]interface{}{
			"prompt_tokens":     ctx.InputTokens,
			"completion_tokens": ctx.OutputTokens,
			"total_tokens":      ctx.InputTokens + ctx.OutputTokens,
		}
		if evt.Response != nil && evt.Response.Usage.TotalTokens > 0 {
			usage["total_tokens"] = evt.Response.Usage.TotalTokens
		}
		content := ""
		if evt.Response != nil {
			content = openAI2MissingOutputText(ctx, evt.Response.Output)
		}
		return buildOpenAIChunkWithUsage(ctx.MessageID, model, content, nil, finishReason, usage)
	}

	return nil, nil
}

func extractOpenAI2Text(content interface{}) string {
	arr, ok := content.([]interface{})
	if !ok {
		return ""
	}
	var parts []string
	for _, part := range arr {
		partMap, ok := part.(map[string]interface{})
		if !ok {
			continue
		}
		if partMap["type"] == "input_text" || partMap["type"] == "output_text" || partMap["type"] == "summary_text" || partMap["type"] == "reasoning_text" {
			if text, ok := partMap["text"].(string); ok {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "")
}
