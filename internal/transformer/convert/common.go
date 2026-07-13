package convert

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lich0821/ccNexus/internal/transformer"
)

// cleanSchemaForGemini removes fields not supported by Gemini API
func cleanSchemaForGemini(schema interface{}) interface{} {
	m, ok := schema.(map[string]interface{})
	if !ok {
		return schema
	}
	// Remove unsupported fields
	delete(m, "additionalProperties")
	delete(m, "$schema")
	if props, ok := m["properties"].(map[string]interface{}); ok {
		for k, v := range props {
			props[k] = cleanSchemaForGemini(v)
		}
	}
	if items, ok := m["items"]; ok {
		m["items"] = cleanSchemaForGemini(items)
	}
	return m
}

// parseSSE parses SSE event data. Per the SSE spec a single event may carry
// multiple `data:` lines that must be concatenated with newlines; keeping only
// the last line truncates long JSON payloads, tool-call arguments, and error
// bodies that upstreams split across lines.
func parseSSE(data []byte) (eventType, jsonData string) {
	var dataLines []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	jsonData = strings.Join(dataLines, "\n")
	return
}

// buildClaudeEvent builds a Claude SSE event
func buildClaudeEvent(eventType string, data map[string]interface{}) []byte {
	data["type"] = eventType
	jsonData, _ := json.Marshal(data)
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, jsonData))
}

// buildOpenAIChunk builds an OpenAI streaming chunk without usage.
func buildOpenAIChunk(id, model, content string, toolCalls []map[string]interface{}, finish string) ([]byte, error) {
	return buildOpenAIChunkWithUsage(id, model, content, toolCalls, finish, nil)
}

// buildOpenAIChunkWithUsage builds an OpenAI streaming chunk with optional usage.
func buildOpenAIChunkWithUsage(id, model, content string, toolCalls []map[string]interface{}, finish string, usage map[string]interface{}) ([]byte, error) {
	delta := map[string]interface{}{}
	if content != "" {
		delta["content"] = content
	}
	if len(toolCalls) > 0 {
		delta["tool_calls"] = toolCalls
	}

	var finishReason interface{} = nil
	if finish != "" {
		finishReason = finish
	}

	chunk := map[string]interface{}{
		"id": id, "object": "chat.completion.chunk", "model": model,
		"choices": []map[string]interface{}{{"index": 0, "delta": delta, "finish_reason": finishReason}},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	data, _ := json.Marshal(chunk)
	return []byte(fmt.Sprintf("data: %s\n\n", data)), nil
}

func writeOpenAI2StreamEvent(ctx *transformer.StreamContext, result *strings.Builder, evt map[string]interface{}) {
	if ctx != nil {
		ctx.ResponseSequenceNumber++
		if _, ok := evt["sequence_number"]; !ok {
			evt["sequence_number"] = ctx.ResponseSequenceNumber
		}
	}
	data, _ := json.Marshal(evt)
	result.WriteString(fmt.Sprintf("data: %s\n\n", data))
}

func ensureOpenAI2StreamState(ctx *transformer.StreamContext) {
	if ctx == nil {
		return
	}
	if ctx.ResponseOutputItemIDByIndex == nil {
		ctx.ResponseOutputItemIDByIndex = make(map[int]string)
	}
	if ctx.ResponseTextByIndex == nil {
		ctx.ResponseTextByIndex = make(map[int]string)
	}
	if ctx.ResponseToolCallIDByIndex == nil {
		ctx.ResponseToolCallIDByIndex = make(map[int]string)
	}
	if ctx.ResponseToolNameByIndex == nil {
		ctx.ResponseToolNameByIndex = make(map[int]string)
	}
	if ctx.ResponseToolArgumentsByIndex == nil {
		ctx.ResponseToolArgumentsByIndex = make(map[int]string)
	}
	if ctx.ResponseToolAddedByIndex == nil {
		ctx.ResponseToolAddedByIndex = make(map[int]bool)
	}
	if ctx.ResponseToolChatIndexByOutputIndex == nil {
		ctx.ResponseToolChatIndexByOutputIndex = make(map[int]int)
	}
	if ctx.ResponseReasoningByIndex == nil {
		ctx.ResponseReasoningByIndex = make(map[int]string)
	}
	if ctx.ClaudeToolBlockIndexByKey == nil {
		ctx.ClaudeToolBlockIndexByKey = make(map[int]int)
	}
	if ctx.ClaudeToolIDByKey == nil {
		ctx.ClaudeToolIDByKey = make(map[int]string)
	}
	if ctx.ClaudeToolNameByKey == nil {
		ctx.ClaudeToolNameByKey = make(map[int]string)
	}
	if ctx.ClaudeToolArgumentsByKey == nil {
		ctx.ClaudeToolArgumentsByKey = make(map[int]string)
	}
	if ctx.ClaudeToolStartedByKey == nil {
		ctx.ClaudeToolStartedByKey = make(map[int]bool)
	}
	if ctx.ClaudeToolDoneByKey == nil {
		ctx.ClaudeToolDoneByKey = make(map[int]bool)
	}
}

func ensureClaudeToolState(ctx *transformer.StreamContext) {
	ensureOpenAI2StreamState(ctx)
}

func recordClaudeToolCall(ctx *transformer.StreamContext, key int, callID, name string) {
	ensureClaudeToolState(ctx)
	if ctx == nil {
		return
	}
	if strings.TrimSpace(callID) != "" {
		ctx.ClaudeToolIDByKey[key] = callID
	}
	if strings.TrimSpace(name) != "" {
		ctx.ClaudeToolNameByKey[key] = name
	}
}

func recordClaudeToolArguments(ctx *transformer.StreamContext, key int, delta string) {
	ensureClaudeToolState(ctx)
	if ctx == nil || delta == "" {
		return
	}
	ctx.ClaudeToolArgumentsByKey[key] += delta
}

func claudeToolBlockIndex(ctx *transformer.StreamContext, key int) int {
	ensureClaudeToolState(ctx)
	if ctx == nil {
		return key
	}
	if idx, ok := ctx.ClaudeToolBlockIndexByKey[key]; ok {
		return idx
	}
	idx := ctx.ContentIndex
	ctx.ClaudeToolBlockIndexByKey[key] = idx
	ctx.ContentIndex++
	return idx
}

func claudeStartedToolKeys(ctx *transformer.StreamContext) []int {
	ensureClaudeToolState(ctx)
	if ctx == nil {
		return nil
	}
	keys := make([]int, 0, len(ctx.ClaudeToolStartedByKey))
	for key, started := range ctx.ClaudeToolStartedByKey {
		if !started || ctx.ClaudeToolDoneByKey[key] {
			continue
		}
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func openAI2ResponseID(ctx *transformer.StreamContext) string {
	if ctx == nil || strings.TrimSpace(ctx.MessageID) == "" {
		return "resp_stream"
	}
	return ctx.MessageID
}

func openAI2OutputItemID(ctx *transformer.StreamContext, outputIndex int) string {
	ensureOpenAI2StreamState(ctx)
	if ctx == nil {
		return fmt.Sprintf("msg_resp_stream_%d", outputIndex)
	}
	if id := strings.TrimSpace(ctx.ResponseOutputItemIDByIndex[outputIndex]); id != "" {
		return id
	}
	id := fmt.Sprintf("msg_%s_%d", openAI2ResponseID(ctx), outputIndex)
	ctx.ResponseOutputItemIDByIndex[outputIndex] = id
	return id
}

func recordOpenAI2OutputItemID(ctx *transformer.StreamContext, outputIndex int, itemID string) {
	ensureOpenAI2StreamState(ctx)
	if ctx == nil || strings.TrimSpace(itemID) == "" {
		return
	}
	ctx.ResponseOutputItemIDByIndex[outputIndex] = itemID
}

func openAI2CreatedEvent(ctx *transformer.StreamContext) map[string]interface{} {
	return map[string]interface{}{
		"type": "response.created",
		"response": map[string]interface{}{
			"id":     openAI2ResponseID(ctx),
			"object": "response",
			"status": "in_progress",
			"output": []interface{}{},
		},
	}
}

func openAI2MessageItem(ctx *transformer.StreamContext, outputIndex int, status string) map[string]interface{} {
	item := map[string]interface{}{
		"type":   "message",
		"id":     openAI2OutputItemID(ctx, outputIndex),
		"role":   "assistant",
		"status": status,
	}
	if status == "in_progress" {
		item["content"] = []interface{}{}
		return item
	}
	item["content"] = []map[string]interface{}{{
		"type": "output_text",
		"text": openAI2Text(ctx, outputIndex),
	}}
	return item
}

func openAI2TextPart(ctx *transformer.StreamContext, outputIndex int) map[string]interface{} {
	return map[string]interface{}{
		"type": "output_text",
		"text": openAI2Text(ctx, outputIndex),
	}
}

func openAI2Text(ctx *transformer.StreamContext, outputIndex int) string {
	if ctx == nil || ctx.ResponseTextByIndex == nil {
		return ""
	}
	return ctx.ResponseTextByIndex[outputIndex]
}

func openAI2TextFromParts(parts []transformer.OpenAI2ContentPart) string {
	var text strings.Builder
	for _, part := range parts {
		if part.Type == "output_text" && part.Text != "" {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}

func missingOpenAI2Text(ctx *transformer.StreamContext, outputIndex int, fullText string) string {
	if fullText == "" {
		return ""
	}
	current := openAI2Text(ctx, outputIndex)
	if current == fullText {
		return ""
	}
	if current != "" {
		if !strings.HasPrefix(fullText, current) {
			return ""
		}
		fullText = strings.TrimPrefix(fullText, current)
	}
	recordOpenAI2Text(ctx, outputIndex, fullText)
	return fullText
}

func openAI2ReasoningTextFromItem(item transformer.OpenAI2OutputItem) string {
	var text strings.Builder
	appendParts := func(parts []transformer.OpenAI2ContentPart) {
		for _, part := range parts {
			if part.Text == "" {
				continue
			}
			switch part.Type {
			case "summary_text", "reasoning_text", "output_text":
				text.WriteString(part.Text)
			}
		}
	}
	appendParts(item.Summary)
	appendParts(item.Content)
	return text.String()
}

func openAI2MissingOutputText(ctx *transformer.StreamContext, output []transformer.OpenAI2OutputItem) string {
	var text strings.Builder
	for fallbackIndex, item := range output {
		if item.Type != "message" {
			continue
		}
		outputIndex := resolveOpenAI2CompletedOutputIndex(ctx, fallbackIndex, item)
		text.WriteString(missingOpenAI2Text(ctx, outputIndex, openAI2TextFromParts(item.Content)))
	}
	return text.String()
}

func resolveOpenAI2CompletedOutputIndex(ctx *transformer.StreamContext, fallbackIndex int, item transformer.OpenAI2OutputItem) int {
	ensureOpenAI2StreamState(ctx)
	if ctx == nil {
		return fallbackIndex
	}

	itemID := strings.TrimSpace(item.ID)
	if itemID != "" {
		resolvedIndex := -1
		for outputIndex, streamedItemID := range ctx.ResponseOutputItemIDByIndex {
			if strings.TrimSpace(streamedItemID) != itemID {
				continue
			}
			if resolvedIndex < 0 || outputIndex < resolvedIndex {
				resolvedIndex = outputIndex
			}
		}
		if resolvedIndex >= 0 {
			return resolvedIndex
		}
	}

	if item.Type == "function_call" {
		callID := strings.TrimSpace(firstNonEmpty(item.CallID, item.ID))
		if callID != "" {
			resolvedIndex := -1
			for outputIndex, streamedCallID := range ctx.ResponseToolCallIDByIndex {
				if strings.TrimSpace(streamedCallID) != callID {
					continue
				}
				if resolvedIndex < 0 || outputIndex < resolvedIndex {
					resolvedIndex = outputIndex
				}
			}
			if resolvedIndex >= 0 {
				return resolvedIndex
			}
		}
	}

	if item.Type == "message" {
		fullText := openAI2TextFromParts(item.Content)
		bestIndex := -1
		bestLength := -1
		for outputIndex, streamedText := range ctx.ResponseTextByIndex {
			if streamedText == "" || !strings.HasPrefix(fullText, streamedText) {
				continue
			}
			if len(streamedText) > bestLength || (len(streamedText) == bestLength && (bestIndex < 0 || outputIndex < bestIndex)) {
				bestIndex = outputIndex
				bestLength = len(streamedText)
			}
		}
		if bestIndex >= 0 {
			return bestIndex
		}
	}

	return fallbackIndex
}

func recordOpenAI2Text(ctx *transformer.StreamContext, outputIndex int, delta string) {
	ensureOpenAI2StreamState(ctx)
	if ctx == nil || delta == "" {
		return
	}
	ensureOpenAI2TextOutput(ctx, outputIndex)
	ctx.ResponseTextByIndex[outputIndex] += delta
}

func ensureOpenAI2TextOutput(ctx *transformer.StreamContext, outputIndex int) {
	ensureOpenAI2StreamState(ctx)
	if ctx == nil {
		return
	}
	if _, ok := ctx.ResponseTextByIndex[outputIndex]; !ok {
		ctx.ResponseTextByIndex[outputIndex] = ""
	}
	openAI2OutputItemID(ctx, outputIndex)
}

func recordOpenAI2ToolCall(ctx *transformer.StreamContext, outputIndex int, callID, name string) {
	ensureOpenAI2StreamState(ctx)
	if ctx == nil {
		return
	}
	if strings.TrimSpace(callID) != "" {
		ctx.ResponseToolCallIDByIndex[outputIndex] = callID
		ctx.ResponseOutputItemIDByIndex[outputIndex] = callID
	}
	if strings.TrimSpace(name) != "" {
		ctx.ResponseToolNameByIndex[outputIndex] = name
	}
}

func recordOpenAI2ToolArguments(ctx *transformer.StreamContext, outputIndex int, delta string) {
	ensureOpenAI2StreamState(ctx)
	if ctx == nil || delta == "" {
		return
	}
	ctx.ResponseToolArgumentsByIndex[outputIndex] += delta
}

func openAI2ChatToolIndex(ctx *transformer.StreamContext, outputIndex int) int {
	ensureOpenAI2StreamState(ctx)
	if ctx == nil {
		return outputIndex
	}
	if idx, ok := ctx.ResponseToolChatIndexByOutputIndex[outputIndex]; ok {
		return idx
	}
	idx := len(ctx.ResponseToolChatIndexByOutputIndex)
	ctx.ResponseToolChatIndexByOutputIndex[outputIndex] = idx
	return idx
}

func openAI2ToolItem(ctx *transformer.StreamContext, outputIndex int, status string) map[string]interface{} {
	ensureOpenAI2StreamState(ctx)
	callID := ""
	name := ""
	arguments := ""
	if ctx != nil {
		callID = ctx.ResponseToolCallIDByIndex[outputIndex]
		name = ctx.ResponseToolNameByIndex[outputIndex]
		arguments = ctx.ResponseToolArgumentsByIndex[outputIndex]
	}
	return map[string]interface{}{
		"type":      "function_call",
		"id":        firstNonEmpty(callID, openAI2OutputItemID(ctx, outputIndex)),
		"call_id":   firstNonEmpty(callID, openAI2OutputItemID(ctx, outputIndex)),
		"name":      name,
		"arguments": arguments,
		"status":    status,
	}
}

func recordOpenAI2Reasoning(ctx *transformer.StreamContext, outputIndex int, delta string) {
	ensureOpenAI2StreamState(ctx)
	if ctx == nil || delta == "" {
		return
	}
	ctx.ResponseReasoningByIndex[outputIndex] += delta
	openAI2OutputItemID(ctx, outputIndex)
}

func openAI2ReasoningItem(ctx *transformer.StreamContext, outputIndex int, status string) map[string]interface{} {
	text := ""
	if ctx != nil && ctx.ResponseReasoningByIndex != nil {
		text = ctx.ResponseReasoningByIndex[outputIndex]
	}
	return map[string]interface{}{
		"type":    "reasoning",
		"id":      openAI2OutputItemID(ctx, outputIndex),
		"status":  status,
		"summary": []map[string]interface{}{{"type": "summary_text", "text": text}},
	}
}

func openAI2TextDeltaEvent(ctx *transformer.StreamContext, outputIndex int, delta string) map[string]interface{} {
	recordOpenAI2Text(ctx, outputIndex, delta)
	return map[string]interface{}{
		"type":          "response.output_text.delta",
		"output_index":  outputIndex,
		"content_index": 0,
		"item_id":       openAI2OutputItemID(ctx, outputIndex),
		"logprobs":      []interface{}{},
		"delta":         delta,
	}
}

func openAI2TextDoneEvent(ctx *transformer.StreamContext, outputIndex int) map[string]interface{} {
	return map[string]interface{}{
		"type":          "response.output_text.done",
		"output_index":  outputIndex,
		"content_index": 0,
		"item_id":       openAI2OutputItemID(ctx, outputIndex),
		"logprobs":      []interface{}{},
		"text":          openAI2Text(ctx, outputIndex),
	}
}

func openAI2ContentPartAddedEvent(ctx *transformer.StreamContext, outputIndex int) map[string]interface{} {
	ensureOpenAI2TextOutput(ctx, outputIndex)
	return map[string]interface{}{
		"type":          "response.content_part.added",
		"output_index":  outputIndex,
		"content_index": 0,
		"item_id":       openAI2OutputItemID(ctx, outputIndex),
		"part":          map[string]interface{}{"type": "output_text", "text": ""},
	}
}

func openAI2ContentPartDoneEvent(ctx *transformer.StreamContext, outputIndex int) map[string]interface{} {
	return map[string]interface{}{
		"type":          "response.content_part.done",
		"output_index":  outputIndex,
		"content_index": 0,
		"item_id":       openAI2OutputItemID(ctx, outputIndex),
		"part":          openAI2TextPart(ctx, outputIndex),
	}
}

func openAI2CompletedEvent(ctx *transformer.StreamContext, totalTokens int) map[string]interface{} {
	if ctx != nil && totalTokens <= 0 {
		totalTokens = ctx.InputTokens + ctx.OutputTokens
	}
	return map[string]interface{}{
		"type": "response.completed",
		"response": map[string]interface{}{
			"id":     openAI2ResponseID(ctx),
			"object": "response",
			"status": "completed",
			"output": openAI2CompletedOutput(ctx),
			"usage": map[string]interface{}{
				"input_tokens":  inputTokensOrZero(ctx),
				"output_tokens": outputTokensOrZero(ctx),
				"total_tokens":  totalTokens,
			},
		},
	}
}

func inputTokensOrZero(ctx *transformer.StreamContext) int {
	if ctx == nil {
		return 0
	}
	return ctx.InputTokens
}

func outputTokensOrZero(ctx *transformer.StreamContext) int {
	if ctx == nil {
		return 0
	}
	return ctx.OutputTokens
}

func openAI2CompletedOutput(ctx *transformer.StreamContext) []interface{} {
	ensureOpenAI2StreamState(ctx)
	if ctx == nil {
		return []interface{}{}
	}
	indices := map[int]bool{}
	for outputIndex := range ctx.ResponseTextByIndex {
		indices[outputIndex] = true
	}
	for outputIndex := range ctx.ResponseToolCallIDByIndex {
		indices[outputIndex] = true
	}
	for outputIndex := range ctx.ResponseReasoningByIndex {
		indices[outputIndex] = true
	}

	ordered := make([]int, 0, len(indices))
	for outputIndex := range indices {
		ordered = append(ordered, outputIndex)
	}
	sort.Ints(ordered)

	output := make([]interface{}, 0, len(ordered))
	for _, outputIndex := range ordered {
		if _, ok := ctx.ResponseToolCallIDByIndex[outputIndex]; ok {
			if strings.TrimSpace(ctx.ResponseToolCallIDByIndex[outputIndex]) == "" ||
				strings.TrimSpace(ctx.ResponseToolNameByIndex[outputIndex]) == "" {
				continue
			}
			output = append(output, openAI2ToolItem(ctx, outputIndex, "completed"))
			continue
		}
		if _, ok := ctx.ResponseReasoningByIndex[outputIndex]; ok {
			output = append(output, openAI2ReasoningItem(ctx, outputIndex, "completed"))
			continue
		}
		output = append(output, openAI2MessageItem(ctx, outputIndex, "completed"))
	}
	return output
}

// syncGeminiUsageMetadata stores Gemini usage metadata in stream context for later usage emission.
func syncGeminiUsageMetadata(resp *transformer.GeminiResponse, ctx *transformer.StreamContext) {
	if resp == nil || resp.UsageMetadata == nil || ctx == nil {
		return
	}
	if resp.UsageMetadata.PromptTokenCount > 0 {
		ctx.InputTokens = resp.UsageMetadata.PromptTokenCount
	}
	if resp.UsageMetadata.CandidatesTokenCount > 0 {
		ctx.OutputTokens = resp.UsageMetadata.CandidatesTokenCount
	}
}

func currentOpenAIUsage(ctx *transformer.StreamContext) map[string]interface{} {
	if ctx == nil || (ctx.InputTokens == 0 && ctx.OutputTokens == 0) {
		return nil
	}
	return map[string]interface{}{
		"prompt_tokens":     ctx.InputTokens,
		"completion_tokens": ctx.OutputTokens,
		"total_tokens":      ctx.InputTokens + ctx.OutputTokens,
	}
}

func currentClaudeUsage(ctx *transformer.StreamContext) map[string]interface{} {
	if ctx == nil {
		return map[string]interface{}{"input_tokens": 0, "output_tokens": 0}
	}
	return map[string]interface{}{
		"input_tokens":  ctx.InputTokens,
		"output_tokens": ctx.OutputTokens,
	}
}

func parseJSONObject(raw string) (map[string]interface{}, error) {
	args := map[string]interface{}{}
	if strings.TrimSpace(raw) == "" {
		return args, nil
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, err
	}
	object, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("tool arguments must be a JSON object")
	}
	return object, nil
}

func buildGeminiFunctionCallChunk(name string, args map[string]interface{}) []byte {
	chunk := map[string]interface{}{
		"candidates": []map[string]interface{}{
			{"content": map[string]interface{}{"role": "model", "parts": []map[string]interface{}{
				{"functionCall": map[string]interface{}{"name": name, "args": args}},
			}}},
		},
	}
	d, _ := json.Marshal(chunk)
	return []byte(fmt.Sprintf("data: %s\n\n", d))
}

func normalizeOpenAIRequestRoles(req *transformer.OpenAIRequest) {
	if req == nil {
		return
	}
	for i := range req.Messages {
		switch strings.ToLower(strings.TrimSpace(req.Messages[i].Role)) {
		case "developer":
			req.Messages[i].Role = "system"
		default:
			req.Messages[i].Role = strings.ToLower(strings.TrimSpace(req.Messages[i].Role))
		}
	}
}

func extractOpenAI2DeveloperInstructions(input interface{}) (string, interface{}) {
	switch value := input.(type) {
	case []interface{}:
		filtered := make([]interface{}, 0, len(value))
		var instructions []string
		for _, raw := range value {
			item, ok := raw.(map[string]interface{})
			if !ok {
				filtered = append(filtered, raw)
				continue
			}
			itemType := strings.ToLower(strings.TrimSpace(stringFromMap(item, "type")))
			role := strings.ToLower(strings.TrimSpace(stringFromMap(item, "role")))
			if itemType == "message" && role == "developer" {
				if text := strings.TrimSpace(extractOpenAI2Text(item["content"])); text != "" {
					instructions = append(instructions, text)
				}
				continue
			}
			filtered = append(filtered, raw)
		}
		return strings.Join(instructions, "\n"), filtered
	case map[string]interface{}:
		itemType := strings.ToLower(strings.TrimSpace(stringFromMap(value, "type")))
		role := strings.ToLower(strings.TrimSpace(stringFromMap(value, "role")))
		if itemType == "message" && role == "developer" {
			return strings.TrimSpace(extractOpenAI2Text(value["content"])), []interface{}{}
		}
	}
	return "", input
}

func joinInstructionParts(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if text := strings.TrimSpace(part); text != "" {
			cleaned = append(cleaned, text)
		}
	}
	return strings.Join(cleaned, "\n")
}

func mapClaudeStopReasonToOpenAIFinishReason(stopReason string) string {
	switch strings.ToLower(strings.TrimSpace(stopReason)) {
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "stop_sequence", "end_turn", "":
		return "stop"
	default:
		return strings.ToLower(strings.TrimSpace(stopReason))
	}
}

func mapOpenAIFinishReasonToClaudeStopReason(finishReason string) string {
	switch strings.ToLower(strings.TrimSpace(finishReason)) {
	case "tool_calls", "function_call":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "content_filter":
		return "content_filter"
	default:
		return "end_turn"
	}
}

func openAI2StatusForClaudeStopReason(stopReason string) (string, map[string]interface{}) {
	switch strings.ToLower(strings.TrimSpace(stopReason)) {
	case "max_tokens":
		return "incomplete", map[string]interface{}{"reason": "max_output_tokens"}
	case "content_filter":
		return "incomplete", map[string]interface{}{"reason": "content_filter"}
	default:
		return "completed", nil
	}
}

func mapOpenAI2ResponseStopReason(status string, reason string, hasToolCall bool) string {
	if hasToolCall {
		return "tool_use"
	}
	if strings.ToLower(strings.TrimSpace(status)) == "incomplete" {
		switch strings.ToLower(strings.TrimSpace(reason)) {
		case "max_output_tokens", "max_tokens":
			return "max_tokens"
		case "content_filter":
			return "content_filter"
		}
	}
	return "end_turn"
}

func mapClaudeToolChoiceToOpenAIChat(toolChoice interface{}) interface{} {
	if toolChoice == nil {
		return nil
	}
	switch tc := toolChoice.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(tc)) {
		case "any":
			return "required"
		default:
			return strings.ToLower(strings.TrimSpace(tc))
		}
	case map[string]interface{}:
		choiceType := strings.ToLower(strings.TrimSpace(stringFromMap(tc, "type")))
		switch choiceType {
		case "tool":
			name := strings.TrimSpace(stringFromMap(tc, "name"))
			if name == "" {
				return nil
			}
			return map[string]interface{}{"type": "function", "function": map[string]string{"name": name}}
		case "any":
			return "required"
		case "auto", "none":
			return choiceType
		}
	}
	return nil
}

func mapOpenAIToolChoiceToClaude(toolChoice interface{}) interface{} {
	if toolChoice == nil {
		return nil
	}
	switch tc := toolChoice.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(tc)) {
		case "required":
			return map[string]interface{}{"type": "any"}
		case "none", "auto":
			return map[string]interface{}{"type": strings.ToLower(strings.TrimSpace(tc))}
		}
	case map[string]interface{}:
		choiceType := strings.ToLower(strings.TrimSpace(stringFromMap(tc, "type")))
		switch choiceType {
		case "function":
			if fn, ok := tc["function"].(map[string]interface{}); ok {
				if name := strings.TrimSpace(stringFromMap(fn, "name")); name != "" {
					return map[string]interface{}{"type": "tool", "name": name}
				}
			}
			if name := strings.TrimSpace(stringFromMap(tc, "name")); name != "" {
				return map[string]interface{}{"type": "tool", "name": name}
			}
		case "tool":
			if name := strings.TrimSpace(stringFromMap(tc, "name")); name != "" {
				return map[string]interface{}{"type": "tool", "name": name}
			}
		case "none", "auto":
			return map[string]interface{}{"type": choiceType}
		case "required", "any":
			return map[string]interface{}{"type": "any"}
		}
	}
	return nil
}

func mapOpenAI2ToolChoiceToClaudeChoice(toolChoice interface{}) interface{} {
	if toolChoice == nil {
		return nil
	}
	switch tc := toolChoice.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(tc)) {
		case "required":
			return map[string]interface{}{"type": "any"}
		case "none", "auto":
			return map[string]interface{}{"type": strings.ToLower(strings.TrimSpace(tc))}
		}
	case map[string]interface{}:
		choiceType := strings.ToLower(strings.TrimSpace(stringFromMap(tc, "type")))
		if choiceType == "function" || choiceType == "tool" {
			if name := strings.TrimSpace(stringFromMap(tc, "name")); name != "" {
				return map[string]interface{}{"type": "tool", "name": name}
			}
		}
		if choiceType == "none" || choiceType == "auto" {
			return map[string]interface{}{"type": choiceType}
		}
		if choiceType == "required" || choiceType == "any" {
			return map[string]interface{}{"type": "any"}
		}
	}
	return nil
}

func geminiToolConfigFromToolChoice(toolChoice interface{}) map[string]interface{} {
	mode := "AUTO"
	var allowed []string
	if toolChoice != nil {
		switch tc := toolChoice.(type) {
		case string:
			switch strings.ToLower(strings.TrimSpace(tc)) {
			case "none":
				mode = "NONE"
			case "required", "any":
				mode = "ANY"
			case "auto":
				mode = "AUTO"
			}
		case map[string]interface{}:
			choiceType := strings.ToLower(strings.TrimSpace(stringFromMap(tc, "type")))
			switch choiceType {
			case "none":
				mode = "NONE"
			case "required", "any":
				mode = "ANY"
			case "auto":
				mode = "AUTO"
			case "function", "tool":
				mode = "ANY"
				if fn, ok := tc["function"].(map[string]interface{}); ok {
					if name := strings.TrimSpace(stringFromMap(fn, "name")); name != "" {
						allowed = append(allowed, name)
					}
				}
				if name := strings.TrimSpace(stringFromMap(tc, "name")); name != "" {
					allowed = append(allowed, name)
				}
			}
		}
	}
	calling := map[string]interface{}{"mode": mode}
	if len(allowed) > 0 {
		names := make([]interface{}, 0, len(allowed))
		for _, name := range allowed {
			names = append(names, name)
		}
		calling["allowedFunctionNames"] = names
	}
	return map[string]interface{}{"functionCallingConfig": calling}
}

// extractSystemText extracts text from Claude system prompt
func extractSystemText(system interface{}) string {
	switch s := system.(type) {
	case string:
		return s
	case []interface{}:
		var parts []string
		for _, block := range s {
			if m, ok := block.(map[string]interface{}); ok {
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// FilterNonResponsesStreamEvent strips internal control events and SSE events
// that lack a Responses API "type" field from a raw event buffer. Some upstream
// Responses API endpoints
// (e.g. opencodex.uk) prepend a spurious chat.completion.chunk before the
// first response.created event. The Python openai SDK's ResponseStreamState
// raises RuntimeError("Expected to have received `response.created` before
// `None`") when the first parsed event has no type, cancelling the connection.
func FilterNonResponsesStreamEvent(event []byte) []byte {
	_, jsonData := parseSSE(event)
	if jsonData == "" || jsonData == "[DONE]" {
		return event
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &payload); err != nil {
		return event
	}
	if eventType, _ := payload["type"].(string); eventType == "codex.rate_limits" {
		return nil
	}
	if _, hasType := payload["type"]; hasType {
		return event
	}
	// No "type" field → not a Responses API event; drop it silently.
	return nil
}
