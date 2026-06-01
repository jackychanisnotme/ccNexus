package convert

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/transformer"
)

func TestOpenAI2RespToClaudeWithThinking(t *testing.T) {
	openai2Resp := `{
		"id": "resp_1",
		"object": "response",
		"status": "completed",
		"output": [{
			"type": "message",
			"role": "assistant",
			"content": [{
				"type": "output_text",
				"text": "<think>Reason</think>Answer"
			}]
		}],
		"usage": {
			"input_tokens": 3,
			"output_tokens": 5,
			"total_tokens": 8
		}
	}`

	claudeRespBytes, err := OpenAI2RespToClaude([]byte(openai2Resp))
	if err != nil {
		t.Fatalf("OpenAI2RespToClaude failed: %v", err)
	}

	var claudeResp map[string]interface{}
	if err := json.Unmarshal(claudeRespBytes, &claudeResp); err != nil {
		t.Fatalf("Failed to unmarshal Claude response: %v", err)
	}

	content, ok := claudeResp["content"].([]interface{})
	if !ok {
		t.Fatalf("Expected content to be an array, got %T", claudeResp["content"])
	}
	if len(content) != 2 {
		t.Fatalf("Expected 2 content blocks, got %d", len(content))
	}
	if content[0].(map[string]interface{})["type"] != "thinking" {
		t.Fatalf("Expected first block thinking, got %v", content[0])
	}
	if content[1].(map[string]interface{})["type"] != "text" {
		t.Fatalf("Expected second block text, got %v", content[1])
	}
}

func TestOpenAI2StreamToClaudeWithThinking(t *testing.T) {
	ctx := transformer.NewStreamContext()
	ctx.ModelName = "claude-3-sonnet-20240229"

	chunks := []string{
		`data: {"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress"}}`,
		`data: {"type":"response.output_text.delta","delta":"<think>Reason</think>Hello"}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed"}}`,
		`data: [DONE]`,
	}

	var allEvents []string
	for _, chunk := range chunks {
		events, err := OpenAI2StreamToClaude([]byte(chunk), ctx)
		if err != nil {
			t.Fatalf("OpenAI2StreamToClaude failed: %v", err)
		}
		if events != nil {
			allEvents = append(allEvents, string(events))
		}
	}

	fullEvents := strings.Join(allEvents, "")
	if !strings.Contains(fullEvents, "\"type\":\"thinking\"") {
		t.Fatalf("Expected thinking block start, but not found")
	}
	if !strings.Contains(fullEvents, "\"thinking\":\"Reason\"") {
		t.Fatalf("Expected thinking delta 'Reason', but not found")
	}
	if !strings.Contains(fullEvents, "\"text\":\"Hello\"") {
		t.Fatalf("Expected text delta 'Hello', but not found")
	}
	if strings.Contains(fullEvents, "<think>") || strings.Contains(fullEvents, "</think>") {
		t.Fatalf("Unexpected think tags leaked into output")
	}
}

func TestOpenAI2StreamToClaudeCompletesWithoutDone(t *testing.T) {
	ctx := transformer.NewStreamContext()
	ctx.ModelName = "claude-3-sonnet-20240229"

	chunks := []string{
		`data: {"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress"}}`,
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed"}}`,
	}

	var allEvents []string
	for _, chunk := range chunks {
		events, err := OpenAI2StreamToClaude([]byte(chunk), ctx)
		if err != nil {
			t.Fatalf("OpenAI2StreamToClaude failed: %v", err)
		}
		if events != nil {
			allEvents = append(allEvents, string(events))
		}
	}

	fullEvents := strings.Join(allEvents, "")
	if !strings.Contains(fullEvents, "\"type\":\"message_delta\"") {
		t.Fatalf("Expected message_delta in transformed events, got: %s", fullEvents)
	}
	if !strings.Contains(fullEvents, "event: message_stop") {
		t.Fatalf("Expected message_stop when response.completed arrives without [DONE], got: %s", fullEvents)
	}
}

func TestOpenAI2StreamToClaudeEmitsCompletedOnlyOutput(t *testing.T) {
	ctx := transformer.NewStreamContext()
	ctx.ModelName = "claude-3-sonnet-20240229"

	chunks := []string{
		`data: {"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress"}}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"late answer"}]}]}}`,
	}

	var allEvents []string
	for _, chunk := range chunks {
		events, err := OpenAI2StreamToClaude([]byte(chunk), ctx)
		if err != nil {
			t.Fatalf("OpenAI2StreamToClaude failed: %v", err)
		}
		if events != nil {
			allEvents = append(allEvents, string(events))
		}
	}

	fullEvents := strings.Join(allEvents, "")
	if !strings.Contains(fullEvents, `"text":"late answer"`) {
		t.Fatalf("expected completed-only text to be emitted, got: %s", fullEvents)
	}
	if !strings.Contains(fullEvents, "event: message_stop") {
		t.Fatalf("expected message_stop, got: %s", fullEvents)
	}
}

func TestOpenAI2StreamToClaudePropagatesUsageFromCompleted(t *testing.T) {
	ctx := transformer.NewStreamContext()
	ctx.ModelName = "claude-3-sonnet-20240229"

	chunks := []string{
		`data: {"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress"}}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}}`,
	}

	var allEvents []string
	for _, chunk := range chunks {
		events, err := OpenAI2StreamToClaude([]byte(chunk), ctx)
		if err != nil {
			t.Fatalf("OpenAI2StreamToClaude failed: %v", err)
		}
		if events != nil {
			allEvents = append(allEvents, string(events))
		}
	}

	fullEvents := strings.Join(allEvents, "")
	if !strings.Contains(fullEvents, `"usage":{"output_tokens":3}`) {
		t.Fatalf("expected message_delta usage output_tokens=3, got: %s", fullEvents)
	}
	if ctx.InputTokens != 7 || ctx.OutputTokens != 3 {
		t.Fatalf("expected context usage input=7 output=3, got input=%d output=%d", ctx.InputTokens, ctx.OutputTokens)
	}
}

func TestClaudeReqToOpenAI2PreservesToolChain(t *testing.T) {
	claudeReq := `{
		"model": "claude-sonnet-4-20250514",
		"stream": false,
		"messages": [
			{"role":"user","content":"请写文件"},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Write","input":{"file_path":"/tmp/a.txt","content":"hello"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}
		],
		"tools": [
			{"name":"Write","description":"Write file","input_schema":{"type":"object","properties":{"file_path":{"type":"string"},"content":{"type":"string"}},"required":["file_path","content"]}}
		]
	}`

	reqBytes, err := ClaudeReqToOpenAI2([]byte(claudeReq), "gpt-4.1")
	if err != nil {
		t.Fatalf("ClaudeReqToOpenAI2 failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}

	input, ok := req["input"].([]interface{})
	if !ok {
		t.Fatalf("input should be array, got %T", req["input"])
	}
	if len(input) != 3 {
		t.Fatalf("expected 3 input items, got %d", len(input))
	}

	functionCall, ok := input[1].(map[string]interface{})
	if !ok || functionCall["type"] != "function_call" {
		t.Fatalf("expected input[1] function_call, got %#v", input[1])
	}
	if functionCall["call_id"] != "toolu_1" {
		t.Fatalf("expected call_id toolu_1, got %#v", functionCall["call_id"])
	}
	if _, hasID := functionCall["id"]; hasID {
		t.Fatalf("function_call.id should not be set for upstream compatibility, got %#v", functionCall["id"])
	}

	argsStr, _ := functionCall["arguments"].(string)
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
		t.Fatalf("function arguments is not valid json: %v, raw=%s", err, argsStr)
	}
	if args["file_path"] != "/tmp/a.txt" {
		t.Fatalf("unexpected function arguments: %#v", args)
	}

	functionOutput, ok := input[2].(map[string]interface{})
	if !ok || functionOutput["type"] != "function_call_output" {
		t.Fatalf("expected input[2] function_call_output, got %#v", input[2])
	}
	if functionOutput["call_id"] != "toolu_1" {
		t.Fatalf("expected output call_id toolu_1, got %#v", functionOutput["call_id"])
	}
	if functionOutput["output"] != "ok" {
		t.Fatalf("expected output ok, got %#v", functionOutput["output"])
	}

	if strings.Contains(string(reqBytes), "[Tool Call:") || strings.Contains(string(reqBytes), "[Tool Result:") {
		t.Fatalf("found legacy pseudo tool text in transformed request: %s", string(reqBytes))
	}
}

func TestClaudeReqToOpenAI2InjectsReasoningEffort(t *testing.T) {
	claudeReq := `{
		"model": "claude-sonnet-4-20250514",
		"stream": true,
		"messages": [{"role":"user","content":"test"}]
	}`

	reqBytes, err := ClaudeReqToOpenAI2WithThinking([]byte(claudeReq), "gpt-5.5", "high")
	if err != nil {
		t.Fatalf("ClaudeReqToOpenAI2WithThinking failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}

	reasoning, ok := req["reasoning"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected reasoning object, got %#v", req["reasoning"])
	}
	if reasoning["effort"] != "high" {
		t.Fatalf("expected reasoning.effort=high, got %#v", reasoning["effort"])
	}
}

func TestClaudeReqToOpenAI2SkipsReasoningEffortWhenOff(t *testing.T) {
	claudeReq := `{
		"model": "claude-sonnet-4-20250514",
		"stream": true,
		"messages": [{"role":"user","content":"test"}]
	}`

	reqBytes, err := ClaudeReqToOpenAI2WithThinking([]byte(claudeReq), "gpt-5.5", "off")
	if err != nil {
		t.Fatalf("ClaudeReqToOpenAI2WithThinking failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}
	if _, ok := req["reasoning"]; ok {
		t.Fatalf("did not expect reasoning when thinking is off, got %#v", req["reasoning"])
	}
}

func TestOpenAI2RespToClaudeFallbackToItemID(t *testing.T) {
	openai2Resp := `{
		"id":"resp_1",
		"object":"response",
		"status":"completed",
		"output":[{"type":"function_call","id":"fc_123","name":"Write","arguments":"{\"file_path\":\"/tmp/a.txt\"}"}],
		"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}
	}`

	claudeRespBytes, err := OpenAI2RespToClaude([]byte(openai2Resp))
	if err != nil {
		t.Fatalf("OpenAI2RespToClaude failed: %v", err)
	}

	var claudeResp map[string]interface{}
	if err := json.Unmarshal(claudeRespBytes, &claudeResp); err != nil {
		t.Fatalf("unmarshal claude resp failed: %v", err)
	}

	content, ok := claudeResp["content"].([]interface{})
	if !ok || len(content) != 1 {
		t.Fatalf("unexpected content: %#v", claudeResp["content"])
	}

	toolUse, ok := content[0].(map[string]interface{})
	if !ok {
		t.Fatalf("tool_use item type invalid: %#v", content[0])
	}
	if toolUse["type"] != "tool_use" {
		t.Fatalf("expected tool_use type, got %#v", toolUse["type"])
	}
	if toolUse["id"] != "fc_123" {
		t.Fatalf("expected tool_use id from item.id fallback, got %#v", toolUse["id"])
	}
}

func TestClaudeReqToOpenAI2MapsToolChoiceAnyToRequired(t *testing.T) {
	claudeReq := `{
		"model": "claude-sonnet-4-20250514",
		"stream": true,
		"messages": [{"role":"user","content":"test"}],
		"tools": [{"name":"Write","description":"Write file","input_schema":{"type":"object"}}],
		"tool_choice": {"type":"any"}
	}`

	reqBytes, err := ClaudeReqToOpenAI2([]byte(claudeReq), "gpt-4.1")
	if err != nil {
		t.Fatalf("ClaudeReqToOpenAI2 failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}

	if req["tool_choice"] != "required" {
		t.Fatalf("expected tool_choice=required, got %#v", req["tool_choice"])
	}
	if _, ok := req["store"]; ok {
		t.Fatalf("did not expect store in generic claude->openai2 conversion, got %#v", req["store"])
	}
	if _, ok := req["instructions"]; ok {
		t.Fatalf("did not expect instructions without system prompt, got %#v", req["instructions"])
	}
}

func TestClaudeReqToOpenAI2MapsNamedToolChoice(t *testing.T) {
	claudeReq := `{
		"model": "claude-sonnet-4-20250514",
		"stream": true,
		"messages": [{"role":"user","content":"test"}],
		"tools": [{"name":"Write","description":"Write file","input_schema":{"type":"object"}}],
		"tool_choice": {"type":"tool","name":"Write"}
	}`

	reqBytes, err := ClaudeReqToOpenAI2([]byte(claudeReq), "gpt-4.1")
	if err != nil {
		t.Fatalf("ClaudeReqToOpenAI2 failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}

	toolChoice, ok := req["tool_choice"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected object tool_choice, got %#v", req["tool_choice"])
	}
	if toolChoice["type"] != "function" || toolChoice["name"] != "Write" {
		t.Fatalf("unexpected tool_choice mapping: %#v", toolChoice)
	}
}

func TestClaudeReqToOpenAI2DefaultsToolChoiceRequiredWhenToolsPresent(t *testing.T) {
	claudeReq := `{
		"model": "claude-sonnet-4-20250514",
		"stream": true,
		"messages": [{"role":"user","content":"test"}],
		"tools": [{"name":"Write","description":"Write file","input_schema":{"type":"object"}}]
	}`

	reqBytes, err := ClaudeReqToOpenAI2([]byte(claudeReq), "gpt-4.1")
	if err != nil {
		t.Fatalf("ClaudeReqToOpenAI2 failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}

	if req["tool_choice"] != "required" {
		t.Fatalf("expected tool_choice=required, got %#v", req["tool_choice"])
	}
}

func TestClaudeReqToOpenAI2DefaultsToolChoiceAutoAfterToolResult(t *testing.T) {
	claudeReq := `{
		"model": "claude-sonnet-4-20250514",
		"stream": true,
		"messages": [
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/tmp/a"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}
		],
		"tools": [{"name":"Read","description":"Read file","input_schema":{"type":"object"}}]
	}`

	reqBytes, err := ClaudeReqToOpenAI2([]byte(claudeReq), "gpt-4.1")
	if err != nil {
		t.Fatalf("ClaudeReqToOpenAI2 failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}

	if req["tool_choice"] != "auto" {
		t.Fatalf("expected tool_choice=auto after tool_result, got %#v", req["tool_choice"])
	}
}

func TestClaudeStreamToOpenAI2ForwardsThinkingAsReasoning(t *testing.T) {
	ctx := transformer.NewStreamContext()
	events := []string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":5}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me think"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hi"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":1}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}

	var out strings.Builder
	for _, ev := range events {
		res, err := ClaudeStreamToOpenAI2([]byte(ev+"\n\n"), ctx)
		if err != nil {
			t.Fatalf("convert error: %v", err)
		}
		out.Write(res)
	}
	got := out.String()

	for _, want := range []string{
		`"type":"response.output_item.added"`,
		`"type":"reasoning"`,
		`"type":"response.reasoning_text.delta"`,
		`"delta":"let me think"`,
		`"type":"response.reasoning_text.done"`,
		`"type":"response.output_text.delta"`,
		`"delta":"hi"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, got)
		}
	}

	// Reasoning must occupy output_index 0 and the message text output_index 1.
	if !strings.Contains(got, `"delta":"let me think","item_id":"msg_msg_1_0","output_index":0`) {
		t.Fatalf("expected reasoning at output_index 0, got:\n%s", got)
	}
	if !strings.Contains(got, `"delta":"hi","item_id":"msg_msg_1_1","logprobs":[],"output_index":1`) {
		t.Fatalf("expected text at output_index 1, got:\n%s", got)
	}
}

func TestOpenAI2ReqToClaudeEnablesThinking(t *testing.T) {
	temp := 0.7
	req := transformer.OpenAI2Request{
		Model:       "gpt-5.5",
		Input:       "hi",
		Stream:      true,
		Temperature: &temp,
		Reasoning:   map[string]interface{}{"effort": "high"},
	}
	raw, _ := json.Marshal(req)
	out, err := OpenAI2ReqToClaude(raw, "claude-opus-4")
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	thinking, ok := got["thinking"].(map[string]interface{})
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("expected thinking enabled, got %v", got["thinking"])
	}
	budget, _ := thinking["budget_tokens"].(float64)
	maxTokens, _ := got["max_tokens"].(float64)
	if budget <= 0 || maxTokens <= budget {
		t.Fatalf("expected max_tokens(%v) > budget_tokens(%v)", maxTokens, budget)
	}
	if _, hasTemp := got["temperature"]; hasTemp {
		t.Fatalf("expected temperature removed when thinking enabled, got %v", got["temperature"])
	}
}

func TestOpenAI2ReqToClaudeThinkingOffDisabled(t *testing.T) {
	req := transformer.OpenAI2Request{
		Model:     "gpt-5.5",
		Input:     "hi",
		Reasoning: map[string]interface{}{"effort": "off"},
	}
	raw, _ := json.Marshal(req)
	out, _ := OpenAI2ReqToClaude(raw, "claude-opus-4")
	var got map[string]interface{}
	_ = json.Unmarshal(out, &got)
	if _, ok := got["thinking"]; ok {
		t.Fatalf("expected no thinking when effort=off, got %v", got["thinking"])
	}
}
