package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lich0821/ccNexus/internal/transformer"
)

// ClaudeReqToOpenAI2 converts Claude request to OpenAI Responses API request
func ClaudeReqToOpenAI2(claudeReq []byte, model string) ([]byte, error) {
	return ClaudeReqToOpenAI2WithThinking(claudeReq, model, "")
}

// ClaudeReqToOpenAI2WithThinking converts Claude request to OpenAI Responses API request
// and injects endpoint-level reasoning effort when configured.
func ClaudeReqToOpenAI2WithThinking(claudeReq []byte, model string, thinking string) ([]byte, error) {
	var req transformer.ClaudeRequest
	if err := json.Unmarshal(claudeReq, &req); err != nil {
		return nil, err
	}

	openai2Req := map[string]interface{}{
		"model":  model,
		"stream": req.Stream,
	}
	thinking = strings.ToLower(strings.TrimSpace(thinking))
	if thinking != "" && thinking != "off" {
		openai2Req["reasoning"] = map[string]interface{}{"effort": thinking}
	}

	// Convert system to instructions
	if req.System != nil {
		openai2Req["instructions"] = extractSystemText(req.System)
	}

	// Convert messages to input
	var input []map[string]interface{}
	for _, msg := range req.Messages {
		switch content := msg.Content.(type) {
		case string:
			textType := "input_text"
			if msg.Role == "assistant" {
				textType = "output_text"
			}
			input = append(input, map[string]interface{}{
				"type": "message",
				"role": msg.Role,
				"content": []map[string]interface{}{
					{
						"type": textType,
						"text": content,
					},
				},
			})
		case []interface{}:
			input = append(input, convertClaudeMessageToOpenAI2Items(content, msg.Role)...)
		}
	}
	openai2Req["input"] = input

	// TODO: max_output_tokens is standard OpenAI Responses API param but some
	// third-party endpoints (e.g. SiliconFlow) don't support it. Skipping for compatibility.

	// Convert tools
	if len(req.Tools) > 0 {
		var tools []map[string]interface{}
		for _, tool := range req.Tools {
			tools = append(tools, map[string]interface{}{
				"type":        "function",
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.InputSchema,
			})
		}
		openai2Req["tools"] = tools

		// Preserve tool forcing semantics for Responses API backends.
		if mapped := mapClaudeToolChoiceToOpenAI2(req.ToolChoice); mapped != nil {
			openai2Req["tool_choice"] = mapped
		} else {
			// For first turn, prefer required to avoid "plan-only" responses.
			// After at least one tool_result exists, switch to auto to prevent
			// forced repeated tool calls in later turns.
			if hasClaudeToolResult(req.Messages) {
				openai2Req["tool_choice"] = "auto"
			} else {
				openai2Req["tool_choice"] = "required"
			}
		}
	}

	return json.Marshal(openai2Req)
}

func mapClaudeToolChoiceToOpenAI2(toolChoice interface{}) interface{} {
	if toolChoice == nil {
		return nil
	}

	switch tc := toolChoice.(type) {
	case map[string]interface{}:
		choiceType, _ := tc["type"].(string)
		switch choiceType {
		case "tool":
			if name, ok := tc["name"].(string); ok && name != "" {
				return map[string]interface{}{
					"type": "function",
					"name": name,
				}
			}
		case "any":
			return "required"
		case "auto":
			return "auto"
		case "none":
			return "none"
		}
	case string:
		switch tc {
		case "any":
			return "required"
		default:
			return tc
		}
	}

	return nil
}

func hasClaudeToolResult(messages []transformer.ClaudeMessage) bool {
	for _, msg := range messages {
		blocks, ok := msg.Content.([]interface{})
		if !ok {
			continue
		}
		for _, block := range blocks {
			m, ok := block.(map[string]interface{})
			if !ok {
				continue
			}
			if t, _ := m["type"].(string); t == "tool_result" {
				return true
			}
		}
	}
	return false
}

// OpenAI2ReqToClaude converts OpenAI Responses API request to Claude request
// claudeThinkingBudget maps the Responses reasoning effort to a Claude extended
// thinking budget. Defaults to a medium budget when effort is unset.
func claudeThinkingBudget(reasoning map[string]interface{}) int {
	effort := ""
	if reasoning != nil {
		effort, _ = reasoning["effort"].(string)
	}
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none", "off":
		return 0
	case "low", "minimal":
		return 2048
	case "high":
		return 16384
	case "xhigh":
		return 24576
	default:
		return 8192
	}
}

func OpenAI2ReqToClaude(openai2Req []byte, model string) ([]byte, error) {
	var req transformer.OpenAI2Request
	if err := json.Unmarshal(openai2Req, &req); err != nil {
		return nil, err
	}

	claudeReq := map[string]interface{}{
		"model":      model,
		"max_tokens": 8192,
		"stream":     req.Stream,
	}

	if req.Instructions != "" {
		claudeReq["system"] = req.Instructions
	}
	if req.MaxOutputTokens > 0 {
		claudeReq["max_tokens"] = req.MaxOutputTokens
	}
	if req.Temperature != nil {
		claudeReq["temperature"] = *req.Temperature
	}

	// Enable Claude extended thinking so Opus streams thinking deltas during long
	// pre-answer reasoning; without it the upstream stays silent until the final
	// text and Codex cancels the stream for lack of progress. Anthropic requires
	// max_tokens > budget_tokens and forbids a custom temperature when thinking is on.
	if budget := claudeThinkingBudget(req.Reasoning); budget > 0 {
		claudeReq["thinking"] = map[string]interface{}{"type": "enabled", "budget_tokens": budget}
		delete(claudeReq, "temperature")
		maxTokens := budget + 4096
		if existing, ok := claudeReq["max_tokens"].(int); ok && existing > maxTokens {
			maxTokens = existing
		}
		claudeReq["max_tokens"] = maxTokens
	}

	// Convert input to messages
	messages := convertOpenAI2InputToClaude(req.Input)
	claudeReq["messages"] = messages

	// Convert tools
	if len(req.Tools) > 0 {
		var tools []map[string]interface{}
		for _, tool := range req.Tools {
			var inputSchema map[string]interface{}
			switch tool.Type {
			case "function":
				inputSchema = tool.Parameters
			case "custom":
				inputSchema = map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"input": map[string]interface{}{"type": "string", "description": "The input for this tool"},
					},
					"required": []string{"input"},
				}
			default:
				continue
			}
			tools = append(tools, map[string]interface{}{
				"name":         tool.Name,
				"description":  tool.Description,
				"input_schema": inputSchema,
			})
		}
		if len(tools) > 0 {
			claudeReq["tools"] = tools
		}
	}

	return json.Marshal(claudeReq)
}

// ClaudeRespToOpenAI2 converts Claude response to OpenAI Responses API response
func ClaudeRespToOpenAI2(claudeResp []byte) ([]byte, error) {
	var resp transformer.ClaudeResponse
	if err := json.Unmarshal(claudeResp, &resp); err != nil {
		return nil, err
	}

	var outputContent []map[string]interface{}
	var functionCalls []map[string]interface{}

	for _, block := range resp.Content {
		blockMap, ok := block.(map[string]interface{})
		if !ok {
			continue
		}
		switch blockMap["type"] {
		case "text":
			outputContent = append(outputContent, map[string]interface{}{
				"type": "output_text",
				"text": blockMap["text"],
			})
		case "thinking":
			// Skip thinking blocks in response
			continue
		case "tool_use":
			args, _ := json.Marshal(blockMap["input"])
			functionCalls = append(functionCalls, map[string]interface{}{
				"type":      "function_call",
				"id":        blockMap["id"],
				"call_id":   blockMap["id"],
				"name":      blockMap["name"],
				"arguments": string(args),
			})
		}
	}

	var output []map[string]interface{}
	if len(outputContent) > 0 {
		output = append(output, map[string]interface{}{
			"type":    "message",
			"role":    "assistant",
			"content": outputContent,
		})
	}
	output = append(output, functionCalls...)

	openai2Resp := map[string]interface{}{
		"id":     resp.ID,
		"object": "response",
		"status": "completed",
		"output": output,
		"usage": map[string]interface{}{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
			"total_tokens":  resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}

	return json.Marshal(openai2Resp)
}

// OpenAI2RespToClaude converts OpenAI Responses API response to Claude response
func OpenAI2RespToClaude(openai2Resp []byte) ([]byte, error) {
	var resp transformer.OpenAI2Response
	if err := json.Unmarshal(openai2Resp, &resp); err != nil {
		return nil, err
	}

	var content []map[string]interface{}
	stopReason := "end_turn"

	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" {
					content = append(content, splitThinkTaggedText(part.Text)...)
				}
			}
		case "function_call":
			var args map[string]interface{}
			json.Unmarshal([]byte(item.Arguments), &args)
			toolID := item.CallID
			if toolID == "" {
				toolID = item.ID
			}
			content = append(content, map[string]interface{}{
				"type":  "tool_use",
				"id":    toolID,
				"name":  item.Name,
				"input": args,
			})
			stopReason = "tool_use"
		}
	}

	claudeResp := map[string]interface{}{
		"id":          resp.ID,
		"type":        "message",
		"role":        "assistant",
		"content":     content,
		"stop_reason": stopReason,
		"usage": map[string]interface{}{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
		},
	}

	return json.Marshal(claudeResp)
}

// ClaudeStreamToOpenAI2 converts Claude SSE event to OpenAI Responses stream event
func ClaudeStreamToOpenAI2(event []byte, ctx *transformer.StreamContext) ([]byte, error) {
	eventType, jsonData := parseSSE(event)
	if jsonData == "" {
		return nil, nil
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return nil, nil
	}

	// Check for error response
	if errType, ok := data["type"].(string); ok && errType == "error" {
		if errData, ok := data["error"].(map[string]interface{}); ok {
			if msg, ok := errData["message"].(string); ok {
				return nil, fmt.Errorf("upstream error: %s", msg)
			}
		}
	}

	var result strings.Builder
	writeEvent := func(evt map[string]interface{}) {
		writeOpenAI2StreamEvent(ctx, &result, evt)
	}
	// closeReasoning emits the Responses reasoning close events once, when a
	// non-thinking block begins or the message ends.
	closeReasoning := func() {
		if !ctx.ReasoningOutputStarted || ctx.ReasoningOutputDone {
			return
		}
		writeEvent(map[string]interface{}{
			"type":          "response.reasoning_text.done",
			"output_index":  ctx.ReasoningOutputIndex,
			"content_index": 0,
			"item_id":       openAI2OutputItemID(ctx, ctx.ReasoningOutputIndex),
			"text":          ctx.ResponseReasoningByIndex[ctx.ReasoningOutputIndex],
		})
		writeEvent(map[string]interface{}{
			"type":         "response.output_item.done",
			"output_index": ctx.ReasoningOutputIndex,
			"item":         openAI2ReasoningItem(ctx, ctx.ReasoningOutputIndex, "completed"),
		})
		ctx.ReasoningOutputDone = true
	}

	switch eventType {
	case "message_start":
		if msg, ok := data["message"].(map[string]interface{}); ok {
			ctx.MessageID, _ = msg["id"].(string)
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				if in, ok := usage["input_tokens"].(float64); ok {
					ctx.InputTokens = int(in)
				}
			}
		}
		writeEvent(openAI2CreatedEvent(ctx))

	case "content_block_start":
		block, ok := data["content_block"].(map[string]interface{})
		if !ok {
			return nil, nil
		}
		idx, _ := data["index"].(float64)
		blockIdx := int(idx)

		switch block["type"] {
		case "thinking":
			if !ctx.ReasoningOutputStarted {
				ctx.ReasoningOutputStarted = true
				ctx.ReasoningOutputIndex = 0
				writeEvent(map[string]interface{}{
					"type":         "response.output_item.added",
					"output_index": ctx.ReasoningOutputIndex,
					"item":         openAI2ReasoningItem(ctx, ctx.ReasoningOutputIndex, "in_progress"),
				})
			}
		case "text":
			closeReasoning()
			ctx.ContentBlockStarted = true
			outputIndex := responseMessageOutputIndex(ctx)
			ctx.ContentIndex = outputIndex
			// output_item.added
			writeEvent(map[string]interface{}{
				"type": "response.output_item.added", "output_index": outputIndex,
				"item": openAI2MessageItem(ctx, outputIndex, "in_progress"),
			})
			// content_part.added
			writeEvent(openAI2ContentPartAddedEvent(ctx, outputIndex))
		case "tool_use":
			closeReasoning()
			ctx.ToolBlockStarted = true
			ctx.ToolIndex = blockIdx
			ctx.CurrentToolID, _ = block["id"].(string)
			ctx.CurrentToolName, _ = block["name"].(string)
			recordOpenAI2ToolCall(ctx, blockIdx, ctx.CurrentToolID, ctx.CurrentToolName)
			// output_item.added for function_call
			writeEvent(map[string]interface{}{
				"type": "response.output_item.added", "output_index": blockIdx,
				"item": openAI2ToolItem(ctx, blockIdx, "in_progress"),
			})
		}

	case "content_block_delta":
		delta, ok := data["delta"].(map[string]interface{})
		if !ok {
			return nil, nil
		}
		switch delta["type"] {
		case "thinking_delta":
			thinking, _ := delta["thinking"].(string)
			if thinking != "" && ctx.ReasoningOutputStarted {
				recordOpenAI2Reasoning(ctx, ctx.ReasoningOutputIndex, thinking)
				writeEvent(map[string]interface{}{
					"type":          "response.reasoning_text.delta",
					"output_index":  ctx.ReasoningOutputIndex,
					"content_index": 0,
					"item_id":       openAI2OutputItemID(ctx, ctx.ReasoningOutputIndex),
					"delta":         thinking,
				})
			}
		case "text_delta":
			text, _ := delta["text"].(string)
			writeEvent(openAI2TextDeltaEvent(ctx, ctx.ContentIndex, text))
		case "input_json_delta":
			partial, _ := delta["partial_json"].(string)
			ctx.ToolArguments += partial
			recordOpenAI2ToolArguments(ctx, ctx.ToolIndex, partial)
			writeEvent(map[string]interface{}{
				"type":         "response.function_call_arguments.delta",
				"output_index": ctx.ToolIndex,
				"item_id":      openAI2OutputItemID(ctx, ctx.ToolIndex),
				"delta":        partial,
			})
		}

	case "content_block_stop":
		idx, _ := data["index"].(float64)
		blockIdx := int(idx)

		if ctx.ToolBlockStarted && blockIdx == ctx.ToolIndex {
			// function_call_arguments.done
			writeEvent(map[string]interface{}{
				"type":         "response.function_call_arguments.done",
				"output_index": blockIdx,
				"item_id":      openAI2OutputItemID(ctx, blockIdx),
				"name":         ctx.CurrentToolName,
				"arguments":    ctx.ToolArguments,
			})
			// output_item.done for function_call
			writeEvent(map[string]interface{}{
				"type":         "response.output_item.done",
				"output_index": blockIdx,
				"item":         openAI2ToolItem(ctx, blockIdx, "completed"),
			})
			ctx.ToolBlockStarted = false
			ctx.ToolArguments = ""
		} else if ctx.ContentBlockStarted && blockIdx == ctx.ContentIndex {
			// output_text.done
			writeEvent(openAI2TextDoneEvent(ctx, blockIdx))
			// content_part.done
			writeEvent(openAI2ContentPartDoneEvent(ctx, blockIdx))
			// output_item.done
			writeEvent(map[string]interface{}{
				"type":         "response.output_item.done",
				"output_index": blockIdx,
				"item":         openAI2MessageItem(ctx, blockIdx, "completed"),
			})
			ctx.ContentBlockStarted = false
		} else if ctx.ReasoningOutputStarted && !ctx.ReasoningOutputDone && blockIdx == ctx.ReasoningOutputIndex {
			closeReasoning()
		}

	case "message_delta":
		if usage, ok := data["usage"].(map[string]interface{}); ok {
			if out, ok := usage["output_tokens"].(float64); ok {
				ctx.OutputTokens = int(out)
			}
		}

	case "message_stop":
		closeReasoning()
		writeEvent(openAI2CompletedEvent(ctx, 0))
		result.WriteString("data: [DONE]\n\n")
	}

	if result.Len() > 0 {
		return []byte(result.String()), nil
	}
	return nil, nil
}

// OpenAI2StreamToClaude converts OpenAI Responses stream event to Claude SSE event
func OpenAI2StreamToClaude(event []byte, ctx *transformer.StreamContext) ([]byte, error) {
	_, jsonData := parseSSE(event)
	if jsonData == "" || jsonData == "[DONE]" {
		if jsonData == "[DONE]" {
			var result []byte
			emitText, emitThinking := makeThinkEmitters(ctx, &result)
			flushThinkTaggedStream(ctx, emitText, emitThinking)
			if ctx.ThinkingBlockStarted {
				result = append(result, buildClaudeEvent("content_block_stop", map[string]interface{}{"index": ctx.ThinkingIndex})...)
				ctx.ThinkingBlockStarted = false
			}
			if ctx.ContentBlockStarted {
				result = append(result, buildClaudeEvent("content_block_stop", map[string]interface{}{"index": ctx.ContentIndex})...)
				ctx.ContentBlockStarted = false
			}
			if ctx.ToolBlockStarted {
				result = append(result, buildClaudeEvent("content_block_stop", map[string]interface{}{"index": ctx.ToolIndex})...)
				ctx.ToolBlockStarted = false
			}
			if !ctx.FinishReasonSent {
				result = append(result, buildClaudeEvent("message_stop", map[string]interface{}{})...)
				ctx.FinishReasonSent = true
			}
			return result, nil
		}
		return nil, nil
	}

	var evt transformer.OpenAI2StreamEvent
	if err := json.Unmarshal([]byte(jsonData), &evt); err != nil {
		return nil, nil
	}

	var result []byte
	appendText := func(text string) {
		if text == "" {
			return
		}
		content := ctx.ThinkingBuffer + text
		ctx.ThinkingBuffer = ""

		emitText, emitThinking := makeThinkEmitters(ctx, &result)
		emitTextWithClose := func(text string) {
			if text == "" {
				return
			}
			if ctx.ThinkingBlockStarted && !ctx.ContentBlockStarted && !ctx.InThinkingTag {
				result = append(result, buildClaudeEvent("content_block_stop", map[string]interface{}{"index": ctx.ThinkingIndex})...)
				ctx.ThinkingBlockStarted = false
			}
			emitText(text)
		}
		emitThinkingWithClose := func(text string) {
			if text == "" {
				return
			}
			emitThinking(text)
			if ctx.ThinkingBlockStarted {
				result = append(result, buildClaudeEvent("content_block_stop", map[string]interface{}{"index": ctx.ThinkingIndex})...)
				ctx.ThinkingBlockStarted = false
			}
		}

		consumeThinkTaggedStream(content, ctx, emitTextWithClose, emitThinkingWithClose)
	}
	appendMissingText := func(outputIndex int, text string) {
		appendText(missingOpenAI2Text(ctx, outputIndex, text))
	}

	switch evt.Type {
	case "response.created":
		if evt.Response != nil {
			ctx.MessageID = evt.Response.ID
			if evt.Response.Usage.InputTokens > 0 {
				ctx.InputTokens = evt.Response.Usage.InputTokens
			}
			if evt.Response.Usage.OutputTokens > 0 {
				ctx.OutputTokens = evt.Response.Usage.OutputTokens
			}
		}
		result = append(result, buildClaudeEvent("message_start", map[string]interface{}{
			"message": map[string]interface{}{
				"id": ctx.MessageID, "type": "message", "role": "assistant", "content": []interface{}{},
				"model": ctx.ModelName, "stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]interface{}{"input_tokens": ctx.InputTokens, "output_tokens": ctx.OutputTokens},
			},
		})...)

	case "response.output_text.delta":
		text := firstNonEmpty(evt.Delta, evt.Text)
		recordOpenAI2Text(ctx, evt.OutputIndex, text)
		appendText(text)

	case "response.output_text.done", "response.content_part.done":
		text := firstNonEmpty(evt.Text, evt.Delta)
		if text == "" && evt.Part != nil {
			text = evt.Part.Text
		}
		appendMissingText(evt.OutputIndex, text)

	case "response.output_item.added":
		if evt.Item != nil && evt.Item.Type == "function_call" {
			if ctx.ThinkingBlockStarted {
				result = append(result, buildClaudeEvent("content_block_stop", map[string]interface{}{"index": ctx.ThinkingIndex})...)
				ctx.ThinkingBlockStarted = false
			}
			// Close text block if open
			if ctx.ContentBlockStarted {
				result = append(result, buildClaudeEvent("content_block_stop", map[string]interface{}{"index": ctx.ContentIndex})...)
				ctx.ContentBlockStarted = false
				ctx.ContentIndex++
			}
			ctx.ToolBlockStarted = true
			ctx.ToolIndex = ctx.ContentIndex
			ctx.CurrentToolID = evt.Item.CallID
			if ctx.CurrentToolID == "" {
				ctx.CurrentToolID = evt.Item.ID
			}
			ctx.CurrentToolName = evt.Item.Name
			ctx.ToolArguments = ""
			result = append(result, buildClaudeEvent("content_block_start", map[string]interface{}{
				"index": ctx.ToolIndex, "content_block": map[string]interface{}{
					"type": "tool_use", "id": ctx.CurrentToolID, "name": ctx.CurrentToolName, "input": map[string]interface{}{},
				},
			})...)
		}

	case "response.function_call_arguments.delta":
		if ctx.ToolBlockStarted {
			ctx.ToolArguments += evt.Delta
			result = append(result, buildClaudeEvent("content_block_delta", map[string]interface{}{
				"index": ctx.ToolIndex, "delta": map[string]interface{}{"type": "input_json_delta", "partial_json": evt.Delta},
			})...)
		}

	case "response.output_item.done":
		if evt.Item != nil && evt.Item.Type == "function_call" && ctx.ToolBlockStarted {
			result = append(result, buildClaudeEvent("content_block_stop", map[string]interface{}{"index": ctx.ToolIndex})...)
			ctx.ToolBlockStarted = false
			ctx.ContentIndex++
		}

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
			for outputIndex, item := range evt.Response.Output {
				if item.Type == "message" {
					appendMissingText(outputIndex, openAI2TextFromParts(item.Content))
				}
			}
		}
		emitText, emitThinking := makeThinkEmitters(ctx, &result)
		flushThinkTaggedStream(ctx, emitText, emitThinking)
		if ctx.ThinkingBlockStarted {
			result = append(result, buildClaudeEvent("content_block_stop", map[string]interface{}{"index": ctx.ThinkingIndex})...)
			ctx.ThinkingBlockStarted = false
		}
		if ctx.ContentBlockStarted {
			result = append(result, buildClaudeEvent("content_block_stop", map[string]interface{}{"index": ctx.ContentIndex})...)
			ctx.ContentBlockStarted = false
		}
		stopReason := "end_turn"
		if ctx.ToolIndex > 0 || ctx.CurrentToolID != "" {
			stopReason = "tool_use"
		}
		result = append(result, buildClaudeEvent("message_delta", map[string]interface{}{
			"delta": map[string]interface{}{"stop_reason": stopReason, "stop_sequence": nil},
			"usage": map[string]interface{}{"output_tokens": ctx.OutputTokens},
		})...)
		result = append(result, buildClaudeEvent("message_stop", map[string]interface{}{})...)
		ctx.FinishReasonSent = true
	}

	return result, nil
}

// Helper functions

func convertClaudeMessageToOpenAI2Items(content []interface{}, role string) []map[string]interface{} {
	var items []map[string]interface{}
	var messageParts []map[string]interface{}
	textType := "input_text"
	if role == "assistant" {
		textType = "output_text"
	}

	flushMessage := func() {
		if len(messageParts) == 0 {
			return
		}
		items = append(items, map[string]interface{}{
			"type":    "message",
			"role":    role,
			"content": messageParts,
		})
		messageParts = nil
	}

	for _, block := range content {
		m, ok := block.(map[string]interface{})
		if !ok {
			continue
		}

		blockType, _ := m["type"].(string)
		switch blockType {
		case "text":
			text, _ := m["text"].(string)
			messageParts = append(messageParts, map[string]interface{}{"type": textType, "text": text})
		case "thinking":
			// Skip thinking blocks - they are Claude's internal reasoning
			continue
		case "tool_use":
			flushMessage()
			callID, _ := m["id"].(string)
			name, _ := m["name"].(string)
			args, _ := json.Marshal(m["input"])
			items = append(items, map[string]interface{}{
				"type":      "function_call",
				"call_id":   callID,
				"name":      name,
				"arguments": string(args),
			})
		case "tool_result":
			flushMessage()
			callID, _ := m["tool_use_id"].(string)
			items = append(items, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  toolResultToString(m["content"]),
			})
		}
	}
	flushMessage()

	return items
}

func toolResultToString(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(data)
	}
}

func convertOpenAI2InputToClaude(input interface{}) []map[string]interface{} {
	var messages []map[string]interface{}

	switch v := input.(type) {
	case string:
		messages = append(messages, map[string]interface{}{"role": "user", "content": v})
	case []interface{}:
		var pendingToolUses []map[string]interface{}
		var pendingToolResults []map[string]interface{}

		for _, item := range v {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			itemType, _ := itemMap["type"].(string)
			switch itemType {
			case "message":
				// Flush pending tool uses before user message
				if len(pendingToolUses) > 0 {
					messages = append(messages, map[string]interface{}{"role": "assistant", "content": pendingToolUses})
					pendingToolUses = nil
				}
				// Flush pending tool results before user message
				if len(pendingToolResults) > 0 {
					messages = append(messages, map[string]interface{}{"role": "user", "content": pendingToolResults})
					pendingToolResults = nil
				}

				role, _ := itemMap["role"].(string)
				content := convertOpenAI2ContentToClaude(itemMap["content"], role)
				messages = append(messages, map[string]interface{}{"role": role, "content": content})

			case "function_call":
				// Convert to Claude tool_use
				callID, _ := itemMap["call_id"].(string)
				if callID == "" {
					callID, _ = itemMap["id"].(string)
				}
				name, _ := itemMap["name"].(string)
				argsStr, _ := itemMap["arguments"].(string)
				var args interface{}
				if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
					args = map[string]interface{}{}
				}
				pendingToolUses = append(pendingToolUses, map[string]interface{}{
					"type": "tool_use", "id": callID, "name": name, "input": args,
				})

			case "function_call_output":
				// Flush pending tool uses first
				if len(pendingToolUses) > 0 {
					messages = append(messages, map[string]interface{}{"role": "assistant", "content": pendingToolUses})
					pendingToolUses = nil
				}
				// Convert to Claude tool_result
				callID, _ := itemMap["call_id"].(string)
				output, _ := itemMap["output"].(string)
				pendingToolResults = append(pendingToolResults, map[string]interface{}{
					"type": "tool_result", "tool_use_id": callID, "content": output,
				})
			}
		}

		// Flush remaining
		if len(pendingToolUses) > 0 {
			messages = append(messages, map[string]interface{}{"role": "assistant", "content": pendingToolUses})
		}
		if len(pendingToolResults) > 0 {
			messages = append(messages, map[string]interface{}{"role": "user", "content": pendingToolResults})
		}
	}
	return messages
}

func convertOpenAI2ContentToClaude(content interface{}, role string) interface{} {
	arr, ok := content.([]interface{})
	if !ok {
		return content
	}

	var result []map[string]interface{}
	for _, part := range arr {
		partMap, ok := part.(map[string]interface{})
		if !ok {
			continue
		}
		switch partMap["type"] {
		case "input_text", "output_text":
			result = append(result, map[string]interface{}{"type": "text", "text": partMap["text"]})
		}
	}

	if len(result) == 1 {
		if text, ok := result[0]["text"].(string); ok {
			return text
		}
	}
	return result
}
