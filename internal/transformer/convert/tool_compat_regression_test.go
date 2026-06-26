package convert

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/transformer"
)

func TestOpenAIReqToClaudePreservesToolChoiceAndDeveloperSemantics(t *testing.T) {
	raw := `{
		"model":"gpt-5.5",
		"messages":[
			{"role":"developer","content":"follow policy"},
			{"role":"user","content":"run lookup"}
		],
		"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],
		"tool_choice":{"type":"function","function":{"name":"lookup"}}
	}`

	out, err := OpenAIReqToClaude([]byte(raw), "claude-opus-4")
	if err != nil {
		t.Fatalf("OpenAIReqToClaude failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}
	if req["system"] != "follow policy" {
		t.Fatalf("expected developer message to become Claude system, got %#v", req["system"])
	}
	choice, ok := req["tool_choice"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected Claude tool_choice object, got %#v", req["tool_choice"])
	}
	if choice["type"] != "tool" || choice["name"] != "lookup" {
		t.Fatalf("unexpected Claude tool_choice: %#v", choice)
	}
}

func TestOpenAIReqToGeminiPreservesToolChoiceAndDeveloperSemantics(t *testing.T) {
	raw := `{
		"model":"gpt-5.5",
		"messages":[
			{"role":"developer","content":"follow policy"},
			{"role":"user","content":"run lookup"}
		],
		"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],
		"tool_choice":"none"
	}`

	out, err := OpenAIReqToGemini([]byte(raw), "gemini-2.5-pro")
	if err != nil {
		t.Fatalf("OpenAIReqToGemini failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}
	system := req["systemInstruction"].(map[string]interface{})
	parts := system["parts"].([]interface{})
	if parts[0].(map[string]interface{})["text"] != "follow policy" {
		t.Fatalf("expected developer content in systemInstruction, got %#v", system)
	}
	toolConfig := req["toolConfig"].(map[string]interface{})
	calling := toolConfig["functionCallingConfig"].(map[string]interface{})
	if calling["mode"] != "NONE" {
		t.Fatalf("expected Gemini tool mode NONE, got %#v", calling)
	}
}

func TestOpenAI2ReqToClaudePreservesToolChoiceAndDeveloperSemantics(t *testing.T) {
	raw := `{
		"model":"gpt-5.5",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"follow policy"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"run lookup"}]}
		],
		"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],
		"tool_choice":"none"
	}`

	out, err := OpenAI2ReqToClaude([]byte(raw), "claude-opus-4")
	if err != nil {
		t.Fatalf("OpenAI2ReqToClaude failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}
	if req["system"] != "follow policy" {
		t.Fatalf("expected developer message to become Claude system, got %#v", req["system"])
	}
	choice, ok := req["tool_choice"].(map[string]interface{})
	if !ok || choice["type"] != "none" {
		t.Fatalf("expected Claude none tool_choice, got %#v", req["tool_choice"])
	}
}

func TestOpenAI2ReqToGeminiPreservesToolChoiceAndDeveloperSemantics(t *testing.T) {
	raw := `{
		"model":"gpt-5.5",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"follow policy"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"run lookup"}]}
		],
		"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],
		"tool_choice":{"type":"function","name":"lookup"}
	}`

	out, err := OpenAI2ReqToGemini([]byte(raw), "gemini-2.5-pro")
	if err != nil {
		t.Fatalf("OpenAI2ReqToGemini failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}
	system := req["systemInstruction"].(map[string]interface{})
	parts := system["parts"].([]interface{})
	if parts[0].(map[string]interface{})["text"] != "follow policy" {
		t.Fatalf("expected developer content in systemInstruction, got %#v", system)
	}
	toolConfig := req["toolConfig"].(map[string]interface{})
	calling := toolConfig["functionCallingConfig"].(map[string]interface{})
	if calling["mode"] != "ANY" {
		t.Fatalf("expected Gemini tool mode ANY, got %#v", calling)
	}
	allowed := calling["allowedFunctionNames"].([]interface{})
	if len(allowed) != 1 || allowed[0] != "lookup" {
		t.Fatalf("expected allowedFunctionNames=[lookup], got %#v", allowed)
	}
}

func TestGeminiReqToClaudeAssignsUniqueIDsForRepeatedFunctionCalls(t *testing.T) {
	raw := `{
		"contents":[
			{"role":"model","parts":[
				{"functionCall":{"name":"lookup","args":{"q":"a"}}},
				{"functionCall":{"name":"lookup","args":{"q":"b"}}}
			]},
			{"role":"user","parts":[
				{"functionResponse":{"name":"lookup","response":{"result":"a"}}},
				{"functionResponse":{"name":"lookup","response":{"result":"b"}}}
			]}
		]
	}`

	out, err := GeminiReqToClaude([]byte(raw), "claude-opus-4")
	if err != nil {
		t.Fatalf("GeminiReqToClaude failed: %v", err)
	}
	var req map[string]interface{}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}
	messages := req["messages"].([]interface{})
	uses := messages[0].(map[string]interface{})["content"].([]interface{})
	results := messages[1].(map[string]interface{})["content"].([]interface{})
	firstID := uses[0].(map[string]interface{})["id"]
	secondID := uses[1].(map[string]interface{})["id"]
	if firstID == "" || secondID == "" || firstID == secondID {
		t.Fatalf("expected unique non-empty tool_use ids, got %#v and %#v", firstID, secondID)
	}
	if results[0].(map[string]interface{})["tool_use_id"] != firstID ||
		results[1].(map[string]interface{})["tool_use_id"] != secondID {
		t.Fatalf("expected tool_result IDs to match generated tool_use IDs, uses=%#v results=%#v", uses, results)
	}
}

func TestClaudeReqToOpenAIDoesNotDropTextFromMixedUserToolResult(t *testing.T) {
	raw := `{
		"model":"claude-opus-4",
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{"q":"x"}}]},
			{"role":"user","content":[
				{"type":"text","text":"before "},
				{"type":"tool_result","tool_use_id":"call_1","content":"result"},
				{"type":"text","text":" after"}
			]}
		]
	}`

	out, err := ClaudeReqToOpenAI([]byte(raw), "gpt-5.5")
	if err != nil {
		t.Fatalf("ClaudeReqToOpenAI failed: %v", err)
	}
	var req map[string]interface{}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}
	messages := req["messages"].([]interface{})
	var userText strings.Builder
	for _, rawMsg := range messages {
		msg := rawMsg.(map[string]interface{})
		if msg["role"] == "user" {
			userText.WriteString(msg["content"].(string))
		}
	}
	if userText.String() != "before  after" {
		t.Fatalf("expected mixed user text to be preserved after tool result conversion, got %q in %#v", userText.String(), messages)
	}
}

func TestClaudeRespToOpenAIMapsMaxTokensStopReasonToLength(t *testing.T) {
	raw := `{
		"id":"msg_1",
		"content":[{"type":"text","text":"partial"}],
		"stop_reason":"max_tokens",
		"usage":{"input_tokens":3,"output_tokens":4}
	}`

	out, err := ClaudeRespToOpenAI([]byte(raw), "gpt-5.5")
	if err != nil {
		t.Fatalf("ClaudeRespToOpenAI failed: %v", err)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal transformed resp failed: %v", err)
	}
	choice := resp["choices"].([]interface{})[0].(map[string]interface{})
	if choice["finish_reason"] != "length" {
		t.Fatalf("expected finish_reason length, got %#v", choice["finish_reason"])
	}
}

func TestOpenAIStreamToClaudeKeepsParallelToolCallsByIndex(t *testing.T) {
	ctx := transformer.NewStreamContext()
	chunks := []string{
		`data: {"id":"chatcmpl_tool_2","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read_file"}},{"index":1,"id":"call_b","type":"function","function":{"name":"write_file"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_tool_2","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"path\":\"out.txt\"}"}},{"index":0,"function":{"arguments":"{\"path\":\"in.txt\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_tool_2","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	var raw strings.Builder
	for _, chunk := range chunks {
		out, err := OpenAIStreamToClaude([]byte(chunk), ctx)
		if err != nil {
			t.Fatalf("OpenAIStreamToClaude failed: %v", err)
		}
		raw.Write(out)
	}

	tools := collectClaudeToolUseStream(t, raw.String())
	if len(tools) != 2 {
		t.Fatalf("expected 2 tool_use blocks, got %#v from stream %s", tools, raw.String())
	}
	if tools["call_a"].name != "read_file" || tools["call_a"].args != `{"path":"in.txt"}` {
		t.Fatalf("unexpected call_a: %#v", tools["call_a"])
	}
	if tools["call_b"].name != "write_file" || tools["call_b"].args != `{"path":"out.txt"}` {
		t.Fatalf("unexpected call_b: %#v", tools["call_b"])
	}
}

func TestOpenAI2StreamToOpenAIKeepsParallelToolCallsByOutputIndex(t *testing.T) {
	ctx := transformer.NewStreamContext()
	events := []string{
		`data: {"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress"}}`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_a","name":"read_file"}}`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_b","name":"write_file"}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"path\":\"out.txt\"}"}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":\"in.txt\"}"}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call_a","name":"read_file"}}`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call_b","name":"write_file"}}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
	}
	var raw strings.Builder
	for _, event := range events {
		out, err := OpenAI2StreamToOpenAI([]byte(event), ctx, "gpt-5.5")
		if err != nil {
			t.Fatalf("OpenAI2StreamToOpenAI failed: %v", err)
		}
		raw.Write(out)
	}
	toolCalls := collectOpenAIChatToolCalls(t, raw.String())
	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 OpenAI tool call chunks, got %#v from %s", toolCalls, raw.String())
	}
	if toolCalls["call_a"].name != "read_file" || toolCalls["call_a"].args != `{"path":"in.txt"}` {
		t.Fatalf("unexpected call_a: %#v", toolCalls["call_a"])
	}
	if toolCalls["call_b"].name != "write_file" || toolCalls["call_b"].args != `{"path":"out.txt"}` {
		t.Fatalf("unexpected call_b: %#v", toolCalls["call_b"])
	}
}

func TestOpenAI2StreamToClaudeKeepsParallelToolCallsByOutputIndex(t *testing.T) {
	ctx := transformer.NewStreamContext()
	events := []string{
		`data: {"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress"}}`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_a","name":"read_file"}}`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_b","name":"write_file"}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"path\":\"out.txt\"}"}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":\"in.txt\"}"}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call_a","name":"read_file"}}`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call_b","name":"write_file"}}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
	}
	var raw strings.Builder
	for _, event := range events {
		out, err := OpenAI2StreamToClaude([]byte(event), ctx)
		if err != nil {
			t.Fatalf("OpenAI2StreamToClaude failed: %v", err)
		}
		raw.Write(out)
	}
	tools := collectClaudeToolUseStream(t, raw.String())
	if len(tools) != 2 {
		t.Fatalf("expected 2 Claude tool_use blocks, got %#v from %s", tools, raw.String())
	}
	if tools["call_a"].name != "read_file" || tools["call_a"].args != `{"path":"in.txt"}` {
		t.Fatalf("unexpected call_a: %#v", tools["call_a"])
	}
	if tools["call_b"].name != "write_file" || tools["call_b"].args != `{"path":"out.txt"}` {
		t.Fatalf("unexpected call_b: %#v", tools["call_b"])
	}
}

func TestOpenAI2StreamToGeminiKeepsParallelToolCallsByOutputIndex(t *testing.T) {
	ctx := transformer.NewStreamContext()
	events := []string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_a","name":"read_file"}}`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_b","name":"write_file"}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"path\":\"out.txt\"}"}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":\"in.txt\"}"}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call_a","name":"read_file"}}`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call_b","name":"write_file"}}`,
	}
	var raw strings.Builder
	for _, event := range events {
		out, err := OpenAI2StreamToGemini([]byte(event), ctx)
		if err != nil {
			t.Fatalf("OpenAI2StreamToGemini failed: %v", err)
		}
		raw.Write(out)
	}
	calls := collectGeminiFunctionCalls(t, raw.String())
	if len(calls) != 2 {
		t.Fatalf("expected 2 Gemini functionCall chunks, got %#v from %s", calls, raw.String())
	}
	if calls["read_file"] != "in.txt" {
		t.Fatalf("expected read_file path in.txt, got %#v", calls)
	}
	if calls["write_file"] != "out.txt" {
		t.Fatalf("expected write_file path out.txt, got %#v", calls)
	}
}

func TestOpenAI2RespToClaudeMapsIncompleteMaxOutputTokens(t *testing.T) {
	raw := `{
		"id":"resp_1",
		"status":"incomplete",
		"incomplete_details":{"reason":"max_output_tokens"},
		"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}],
		"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}
	}`

	out, err := OpenAI2RespToClaude([]byte(raw))
	if err != nil {
		t.Fatalf("OpenAI2RespToClaude failed: %v", err)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal Claude resp failed: %v", err)
	}
	if resp["stop_reason"] != "max_tokens" {
		t.Fatalf("expected stop_reason max_tokens, got %#v", resp["stop_reason"])
	}
}

func TestClaudeStreamToOpenAIEmitsUsageChunk(t *testing.T) {
	ctx := transformer.NewStreamContext()
	events := []string{
		`event: message_start
data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":7}}}`,
		`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":3}}`,
	}
	var raw strings.Builder
	for _, event := range events {
		out, err := ClaudeStreamToOpenAI([]byte(event), ctx, "gpt-5.5")
		if err != nil {
			t.Fatalf("ClaudeStreamToOpenAI failed: %v", err)
		}
		raw.Write(out)
	}
	if !strings.Contains(raw.String(), `"usage":{"completion_tokens":3,"prompt_tokens":7,"total_tokens":10}`) {
		t.Fatalf("expected OpenAI usage chunk, got %s", raw.String())
	}
}

type streamedToolCall struct {
	name string
	args string
}

func collectClaudeToolUseStream(t *testing.T, raw string) map[string]streamedToolCall {
	t.Helper()
	indexToID := map[float64]string{}
	tools := map[string]streamedToolCall{}
	for _, block := range strings.Split(raw, "\n\n") {
		_, jsonData := parseSSE([]byte(block + "\n"))
		if jsonData == "" {
			continue
		}
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
			t.Fatalf("unmarshal Claude SSE event failed: %v in %q", err, block)
		}
		switch event["type"] {
		case "content_block_start":
			contentBlock := event["content_block"].(map[string]interface{})
			if contentBlock["type"] != "tool_use" {
				continue
			}
			id := contentBlock["id"].(string)
			index := event["index"].(float64)
			indexToID[index] = id
			tools[id] = streamedToolCall{name: contentBlock["name"].(string)}
		case "content_block_delta":
			delta := event["delta"].(map[string]interface{})
			if delta["type"] != "input_json_delta" {
				continue
			}
			id := indexToID[event["index"].(float64)]
			tool := tools[id]
			tool.args += delta["partial_json"].(string)
			tools[id] = tool
		}
	}
	return tools
}

func collectOpenAIChatToolCalls(t *testing.T, raw string) map[string]streamedToolCall {
	t.Helper()
	tools := map[string]streamedToolCall{}
	for _, block := range strings.Split(raw, "\n\n") {
		_, jsonData := parseSSE([]byte(block + "\n"))
		if jsonData == "" {
			continue
		}
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
			t.Fatalf("unmarshal OpenAI chunk failed: %v in %q", err, block)
		}
		choices, _ := chunk["choices"].([]interface{})
		if len(choices) == 0 {
			continue
		}
		delta, _ := choices[0].(map[string]interface{})["delta"].(map[string]interface{})
		rawToolCalls, _ := delta["tool_calls"].([]interface{})
		for _, rawToolCall := range rawToolCalls {
			toolCall := rawToolCall.(map[string]interface{})
			id := toolCall["id"].(string)
			fn := toolCall["function"].(map[string]interface{})
			tools[id] = streamedToolCall{name: fn["name"].(string), args: fn["arguments"].(string)}
		}
	}
	return tools
}

func collectGeminiFunctionCalls(t *testing.T, raw string) map[string]string {
	t.Helper()
	calls := map[string]string{}
	for _, block := range strings.Split(raw, "\n\n") {
		_, jsonData := parseSSE([]byte(block + "\n"))
		if jsonData == "" {
			continue
		}
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
			t.Fatalf("unmarshal Gemini chunk failed: %v in %q", err, block)
		}
		candidates, _ := chunk["candidates"].([]interface{})
		for _, rawCandidate := range candidates {
			content, _ := rawCandidate.(map[string]interface{})["content"].(map[string]interface{})
			parts, _ := content["parts"].([]interface{})
			for _, rawPart := range parts {
				part := rawPart.(map[string]interface{})
				functionCall, ok := part["functionCall"].(map[string]interface{})
				if !ok {
					continue
				}
				name := functionCall["name"].(string)
				args := functionCall["args"].(map[string]interface{})
				path, _ := args["path"].(string)
				calls[name] = path
			}
		}
	}
	return calls
}
