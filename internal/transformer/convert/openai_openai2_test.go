package convert

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/transformer"
)

func TestOpenAIReqToOpenAI2DefaultsToolChoiceAutoWhenToolsPresent(t *testing.T) {
	openaiReq := `{
		"model":"gpt-4.1",
		"stream":true,
		"messages":[{"role":"user","content":"test"}],
		"tools":[{"type":"function","function":{"name":"Write","description":"Write file","parameters":{"type":"object"}}}]
	}`

	reqBytes, err := OpenAIReqToOpenAI2([]byte(openaiReq), "gpt-4.1")
	if err != nil {
		t.Fatalf("OpenAIReqToOpenAI2 failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}

	if req["tool_choice"] != "auto" {
		t.Fatalf("expected tool_choice=auto, got %#v", req["tool_choice"])
	}
	if _, ok := req["store"]; ok {
		t.Fatalf("did not expect store in generic openai2 conversion, got %#v", req["store"])
	}
	if _, ok := req["instructions"]; ok {
		t.Fatalf("did not expect instructions without system prompt, got %#v", req["instructions"])
	}
}

func TestOpenAIReqToOpenAI2PreservesReasoningEffort(t *testing.T) {
	openaiReq := `{
		"model":"gpt-5.5",
		"stream":true,
		"reasoning_effort":"high",
		"messages":[{"role":"user","content":"test"}]
	}`

	reqBytes, err := OpenAIReqToOpenAI2([]byte(openaiReq), "gpt-5.5")
	if err != nil {
		t.Fatalf("OpenAIReqToOpenAI2 failed: %v", err)
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

func TestOpenAIReqToOpenAI2ConvertsToolConversation(t *testing.T) {
	openaiReq := `{
		"model":"gpt-5.5",
		"stream":true,
		"messages":[
			{"role":"user","content":"lookup"},
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"symbol\":\"002714\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"牧原股份基本面数据"}
		]
	}`

	reqBytes, err := OpenAIReqToOpenAI2([]byte(openaiReq), "gpt-5.5")
	if err != nil {
		t.Fatalf("OpenAIReqToOpenAI2 failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}

	input := req["input"].([]interface{})
	if len(input) != 3 {
		t.Fatalf("expected 3 input items, got %d: %#v", len(input), input)
	}

	functionCall := input[1].(map[string]interface{})
	if functionCall["type"] != "function_call" {
		t.Fatalf("expected function_call item, got %#v", functionCall)
	}
	if functionCall["call_id"] != "call_1" {
		t.Fatalf("expected call_id=call_1, got %#v", functionCall["call_id"])
	}

	toolOutput := input[2].(map[string]interface{})
	if toolOutput["type"] != "function_call_output" {
		t.Fatalf("expected function_call_output item, got %#v", toolOutput)
	}
	if _, ok := toolOutput["role"]; ok {
		t.Fatalf("did not expect role on function_call_output, got %#v", toolOutput)
	}
	if toolOutput["output"] != "牧原股份基本面数据" {
		t.Fatalf("expected tool output text, got %#v", toolOutput["output"])
	}
}

func TestNormalizeOpenAI2RequestForUpstreamConvertsToolRoleInput(t *testing.T) {
	openai2Req := `{
		"model":"gpt-5.5",
		"stream":true,
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"lookup"}]},
			{"type":"message","role":"assistant","content":[],"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"symbol\":\"002714\"}"}}]},
			{"type":"message","role":"tool","tool_call_id":"call_1","content":"牧原股份基本面数据"}
		]
	}`

	reqBytes, err := NormalizeOpenAI2RequestForUpstream([]byte(openai2Req))
	if err != nil {
		t.Fatalf("NormalizeOpenAI2RequestForUpstream failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal normalized req failed: %v", err)
	}

	input := req["input"].([]interface{})
	for _, rawItem := range input {
		item := rawItem.(map[string]interface{})
		if item["role"] == "tool" {
			t.Fatalf("did not expect tool role after normalization: %#v", item)
		}
	}

	functionCall := input[1].(map[string]interface{})
	if functionCall["type"] != "function_call" {
		t.Fatalf("expected assistant tool_calls to become function_call, got %#v", functionCall)
	}
	toolOutput := input[2].(map[string]interface{})
	if toolOutput["type"] != "function_call_output" || toolOutput["call_id"] != "call_1" {
		t.Fatalf("expected function_call_output with call_id, got %#v", toolOutput)
	}
}

func TestOpenAI2ReqToOpenAIPreservesReasoningEffort(t *testing.T) {
	openai2Req := `{
		"model":"gpt-5.5",
		"stream":true,
		"reasoning":{"effort":"medium"},
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}]
	}`

	reqBytes, err := OpenAI2ReqToOpenAI([]byte(openai2Req), "gpt-5.5")
	if err != nil {
		t.Fatalf("OpenAI2ReqToOpenAI failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}
	if req["reasoning_effort"] != "medium" {
		t.Fatalf("expected reasoning_effort=medium, got %#v", req["reasoning_effort"])
	}
}

func TestOpenAI2ReqToOpenAIMapsDeveloperRoleToSystem(t *testing.T) {
	openai2Req := `{
		"model":"gpt-5.5",
		"stream":false,
		"input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"follow policy"}]}]
	}`

	reqBytes, err := OpenAI2ReqToOpenAI([]byte(openai2Req), "gpt-5.5")
	if err != nil {
		t.Fatalf("OpenAI2ReqToOpenAI failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}
	messages := req["messages"].([]interface{})
	message := messages[0].(map[string]interface{})
	if message["role"] != "system" {
		t.Fatalf("expected developer role to map to system, got %#v", message["role"])
	}
	if message["content"] != "follow policy" {
		t.Fatalf("expected content to be preserved, got %#v", message["content"])
	}
}

func TestOpenAI2ReqToOpenAIPreservesReasoningInput(t *testing.T) {
	openai2Req := `{
		"model":"deepseek-v4-pro",
		"stream":false,
		"input":[
			{"type":"reasoning","summary":[{"type":"summary_text","text":"reason first"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}
		]
	}`

	reqBytes, err := OpenAI2ReqToOpenAI([]byte(openai2Req), "deepseek-v4-pro")
	if err != nil {
		t.Fatalf("OpenAI2ReqToOpenAI failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}
	messages := req["messages"].([]interface{})
	assistant := messages[0].(map[string]interface{})
	if assistant["reasoning_content"] != "reason first" {
		t.Fatalf("expected assistant reasoning_content, got %#v", assistant["reasoning_content"])
	}
	if assistant["content"] != "answer" {
		t.Fatalf("expected assistant content=answer, got %#v", assistant["content"])
	}
}

func TestOpenAIRespToOpenAI2PreservesReasoningContent(t *testing.T) {
	openaiResp := `{
		"id":"chatcmpl_123",
		"object":"chat.completion",
		"model":"deepseek-v4-pro",
		"choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"reasoned","content":"answer"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}
	}`

	respBytes, err := OpenAIRespToOpenAI2([]byte(openaiResp))
	if err != nil {
		t.Fatalf("OpenAIRespToOpenAI2 failed: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal transformed response failed: %v", err)
	}
	output := resp["output"].([]interface{})
	reasoning := output[0].(map[string]interface{})
	if reasoning["type"] != "reasoning" {
		t.Fatalf("expected first output item to be reasoning, got %#v", reasoning)
	}
	summary := reasoning["summary"].([]interface{})[0].(map[string]interface{})
	if summary["text"] != "reasoned" {
		t.Fatalf("expected reasoning summary text, got %#v", summary["text"])
	}
	message := output[1].(map[string]interface{})
	if message["type"] != "message" {
		t.Fatalf("expected second output item to be message, got %#v", message)
	}
}

func TestOpenAI2RespToOpenAIPreservesReasoningContent(t *testing.T) {
	openai2Resp := `{
		"id":"resp_123",
		"object":"response",
		"status":"completed",
		"output":[
			{"type":"reasoning","summary":[{"type":"summary_text","text":"reasoned"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}
		],
		"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}
	}`

	respBytes, err := OpenAI2RespToOpenAI([]byte(openai2Resp), "deepseek-v4-pro")
	if err != nil {
		t.Fatalf("OpenAI2RespToOpenAI failed: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal transformed response failed: %v", err)
	}
	choice := resp["choices"].([]interface{})[0].(map[string]interface{})
	message := choice["message"].(map[string]interface{})
	if message["reasoning_content"] != "reasoned" {
		t.Fatalf("expected reasoning_content=reasoned, got %#v", message["reasoning_content"])
	}
	if message["content"] != "answer" {
		t.Fatalf("expected content=answer, got %#v", message["content"])
	}
}

func TestOpenAI2RespToOpenAIPreservesTotalTokens(t *testing.T) {
	openai2Resp := `{
		"id":"resp_123",
		"object":"response",
		"status":"completed",
		"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
		"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":99}
	}`

	respBytes, err := OpenAI2RespToOpenAI([]byte(openai2Resp), "gpt-4.1")
	if err != nil {
		t.Fatalf("OpenAI2RespToOpenAI failed: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal transformed response failed: %v", err)
	}

	usage, ok := resp["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected usage object, got %#v", resp["usage"])
	}

	if usage["total_tokens"] != float64(99) {
		t.Fatalf("expected total_tokens=99, got %#v", usage["total_tokens"])
	}
}

func TestOpenAI2StreamToOpenAIIncludesUsageOnCompleted(t *testing.T) {
	ctx := transformer.NewStreamContext()

	created := `data: {"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress"}}`
	if out, err := OpenAI2StreamToOpenAI([]byte(created), ctx, "gpt-4.1"); err != nil {
		t.Fatalf("response.created failed: %v", err)
	} else if out != nil {
		t.Fatalf("expected nil output for response.created, got %s", string(out))
	}

	completed := `data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","usage":{"input_tokens":7,"output_tokens":3,"total_tokens":42}}}`
	out, err := OpenAI2StreamToOpenAI([]byte(completed), ctx, "gpt-4.1")
	if err != nil {
		t.Fatalf("response.completed failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected transformed chunk, got nil")
	}

	_, jsonData := parseSSE(out)
	var chunk map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
		t.Fatalf("unmarshal chunk failed: %v, raw=%s", err, jsonData)
	}

	usage, ok := chunk["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected usage in final chunk, got %#v", chunk["usage"])
	}
	if usage["prompt_tokens"] != float64(7) {
		t.Fatalf("expected prompt_tokens=7, got %#v", usage["prompt_tokens"])
	}
	if usage["completion_tokens"] != float64(3) {
		t.Fatalf("expected completion_tokens=3, got %#v", usage["completion_tokens"])
	}
	if usage["total_tokens"] != float64(42) {
		t.Fatalf("expected total_tokens=42, got %#v", usage["total_tokens"])
	}
}

func TestOpenAI2StreamToOpenAIEmitsCompletedOnlyOutput(t *testing.T) {
	ctx := transformer.NewStreamContext()

	completed := `data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"late answer"}]}]}}`
	out, err := OpenAI2StreamToOpenAI([]byte(completed), ctx, "gpt-4.1")
	if err != nil {
		t.Fatalf("response.completed failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected transformed chunk, got nil")
	}

	_, jsonData := parseSSE(out)
	var chunk map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
		t.Fatalf("unmarshal chunk failed: %v, raw=%s", err, jsonData)
	}
	choice := chunk["choices"].([]interface{})[0].(map[string]interface{})
	delta := choice["delta"].(map[string]interface{})
	if delta["content"] != "late answer" {
		t.Fatalf("expected completed-only text in final delta, got %#v", delta)
	}
	if choice["finish_reason"] != "stop" {
		t.Fatalf("expected finish_reason stop, got %#v", choice["finish_reason"])
	}
}

func TestOpenAIStreamToOpenAI2PreservesReasoningDelta(t *testing.T) {
	ctx := transformer.NewStreamContext()

	chunk := `data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"reasoning_content":"think"},"finish_reason":null}]}`
	out, err := OpenAIStreamToOpenAI2([]byte(chunk), ctx)
	if err != nil {
		t.Fatalf("OpenAIStreamToOpenAI2 failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected reasoning stream events, got nil")
	}
	events := string(out)
	if !strings.Contains(events, `"type":"response.reasoning_text.delta"`) {
		t.Fatalf("expected reasoning_text delta event, got %s", events)
	}
	if !strings.Contains(events, `"delta":"think"`) {
		t.Fatalf("expected reasoning delta text, got %s", events)
	}

	finish := `data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	out, err = OpenAIStreamToOpenAI2([]byte(finish), ctx)
	if err != nil {
		t.Fatalf("finish event failed: %v", err)
	}
	if !strings.Contains(string(out), `"type":"response.reasoning_text.done"`) {
		t.Fatalf("expected reasoning_text done event, got %s", string(out))
	}
}

func TestOpenAI2StreamToOpenAIPreservesReasoningDelta(t *testing.T) {
	ctx := transformer.NewStreamContext()
	ctx.MessageID = "resp_1"

	event := `data: {"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"delta":"think"}`
	out, err := OpenAI2StreamToOpenAI([]byte(event), ctx, "deepseek-v4-pro")
	if err != nil {
		t.Fatalf("OpenAI2StreamToOpenAI failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected OpenAI reasoning chunk, got nil")
	}

	_, jsonData := parseSSE(out)
	var chunk map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
		t.Fatalf("unmarshal chunk failed: %v, raw=%s", err, jsonData)
	}
	choice := chunk["choices"].([]interface{})[0].(map[string]interface{})
	delta := choice["delta"].(map[string]interface{})
	if delta["reasoning_content"] != "think" {
		t.Fatalf("expected reasoning_content=think, got %#v", delta["reasoning_content"])
	}
}
