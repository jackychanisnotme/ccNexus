package convert

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/lich0821/ccNexus/internal/transformer"
)

func TestOpenAI2ReqToClaudeReordersInterveningMessageAroundToolChain(t *testing.T) {
	openai2Req := `{
		"model":"gpt-5.5",
		"stream":false,
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"lookup"}]},
			{"type":"function_call","call_id":"call_1","name":"Read","arguments":"{\"path\":\"/tmp/a\"}"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"between"}]},
			{"type":"function_call_output","call_id":"call_1","output":"ok"}
		]
	}`

	reqBytes, err := OpenAI2ReqToClaude([]byte(openai2Req), "claude-opus-4")
	if err != nil {
		t.Fatalf("OpenAI2ReqToClaude failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}

	messages := req["messages"].([]interface{})
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d: %#v", len(messages), messages)
	}

	assistant := messages[1].(map[string]interface{})
	if assistant["role"] != "assistant" {
		t.Fatalf("expected assistant tool_use message at index 1, got %#v", assistant["role"])
	}
	assistantContent := assistant["content"].([]interface{})
	if len(assistantContent) != 1 || assistantContent[0].(map[string]interface{})["type"] != "tool_use" {
		t.Fatalf("expected tool_use at index 1, got %#v", assistantContent)
	}

	toolResult := messages[2].(map[string]interface{})
	if toolResult["role"] != "user" {
		t.Fatalf("expected tool_result user message at index 2, got %#v", toolResult["role"])
	}
	toolResultContent := toolResult["content"].([]interface{})
	if len(toolResultContent) != 2 || toolResultContent[0].(map[string]interface{})["type"] != "tool_result" {
		t.Fatalf("expected tool_result at index 2, got %#v", toolResultContent)
	}
	if toolResultContent[1].(map[string]interface{})["text"] != "between" {
		t.Fatalf("expected intervening text after tool_result, got %#v", toolResultContent)
	}
}

func TestOpenAIReqToClaudeReordersInterveningMessageAroundToolChain(t *testing.T) {
	openaiReq := `{
		"model":"gpt-5.5",
		"stream":false,
		"messages":[
			{"role":"user","content":"lookup"},
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"Read","arguments":"{\"path\":\"/tmp/a\"}"}}]},
			{"role":"user","content":"between"},
			{"role":"tool","tool_call_id":"call_1","content":"ok"}
		]
	}`

	reqBytes, err := OpenAIReqToClaude([]byte(openaiReq), "claude-opus-4")
	if err != nil {
		t.Fatalf("OpenAIReqToClaude failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}

	messages := req["messages"].([]interface{})
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d: %#v", len(messages), messages)
	}

	assistant := messages[1].(map[string]interface{})
	assistantContent := assistant["content"].([]interface{})
	if len(assistantContent) != 1 || assistantContent[0].(map[string]interface{})["type"] != "tool_use" {
		t.Fatalf("expected tool_use at index 1, got %#v", assistantContent)
	}

	toolResult := messages[2].(map[string]interface{})
	if toolResult["role"] != "user" {
		t.Fatalf("expected tool_result user message at index 2, got %#v", toolResult["role"])
	}
	toolResultContent := toolResult["content"].([]interface{})
	if toolResultContent[0].(map[string]interface{})["type"] != "tool_result" {
		t.Fatalf("expected tool_result at index 2, got %#v", toolResult["content"])
	}
	if len(toolResultContent) != 2 || toolResultContent[1].(map[string]interface{})["text"] != "between" {
		t.Fatalf("expected intervening text after tool_result, got %#v", toolResultContent)
	}
}

func TestOpenAI2ReqToClaudeReturnsInvalidToolChainErrorWhenResultMissing(t *testing.T) {
	openai2Req := `{
		"model":"gpt-5.5",
		"stream":false,
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"lookup"}]},
			{"type":"function_call","call_id":"call_1","name":"Read","arguments":"{\"path\":\"/tmp/a\"}"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"between"}]}
		]
	}`

	_, err := OpenAI2ReqToClaude([]byte(openai2Req), "claude-opus-4")
	if err == nil {
		t.Fatalf("expected InvalidToolChainError, got nil")
	}

	var toolErr *InvalidToolChainError
	if !errors.As(err, &toolErr) {
		t.Fatalf("expected InvalidToolChainError, got %T: %v", err, err)
	}
	if toolErr.CallID != "call_1" {
		t.Fatalf("expected call_id call_1, got %#v", toolErr.CallID)
	}
}

func TestOpenAI2ReqToOpenAIReordersInterveningMessageAroundToolChain(t *testing.T) {
	openai2Req := `{
		"model":"gpt-5.5",
		"stream":false,
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"lookup"}]},
			{"type":"function_call","call_id":"call_1","name":"Read","arguments":"{\"path\":\"/tmp/a\"}"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"between"}]},
			{"type":"function_call_output","call_id":"call_1","output":"ok"}
		]
	}`

	reqBytes, err := OpenAI2ReqToOpenAI([]byte(openai2Req), "gpt-5.5")
	if err != nil {
		t.Fatalf("OpenAI2ReqToOpenAI failed: %v", err)
	}

	var req transformer.OpenAIRequest
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}

	if len(req.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d: %#v", len(req.Messages), req.Messages)
	}
	if len(req.Messages[1].ToolCalls) != 1 || req.Messages[1].ToolCalls[0].ID != "call_1" {
		t.Fatalf("expected assistant tool_calls at index 1, got %#v", req.Messages[1])
	}
	if req.Messages[2].Role != "tool" || req.Messages[2].ToolCallID != "call_1" {
		t.Fatalf("expected tool result at index 2, got %#v", req.Messages[2])
	}
	if req.Messages[3].Role != "user" || req.Messages[3].Content != "between" {
		t.Fatalf("expected intervening user message after tool result, got %#v", req.Messages[3])
	}
}

func TestOpenAI2ReqToClaudePreservesParallelToolResults(t *testing.T) {
	openai2Req := `{
		"model":"gpt-5.5",
		"stream":false,
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"lookup"}]},
			{"type":"function_call","call_id":"call_1","name":"Read","arguments":"{\"path\":\"/tmp/a\"}"},
			{"type":"function_call","call_id":"call_2","name":"Read","arguments":"{\"path\":\"/tmp/b\"}"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"between"}]},
			{"type":"function_call_output","call_id":"call_1","output":"a"},
			{"type":"function_call_output","call_id":"call_2","output":"b"}
		]
	}`

	reqBytes, err := OpenAI2ReqToClaude([]byte(openai2Req), "claude-opus-4")
	if err != nil {
		t.Fatalf("OpenAI2ReqToClaude failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}
	messages := req["messages"].([]interface{})
	assistantContent := messages[1].(map[string]interface{})["content"].([]interface{})
	if len(assistantContent) != 2 {
		t.Fatalf("expected 2 tool_use blocks, got %#v", assistantContent)
	}

	resultContent := messages[2].(map[string]interface{})["content"].([]interface{})
	if len(resultContent) != 3 {
		t.Fatalf("expected 2 tool_result blocks plus deferred text, got %#v", resultContent)
	}
	if resultContent[0].(map[string]interface{})["tool_use_id"] != "call_1" ||
		resultContent[1].(map[string]interface{})["tool_use_id"] != "call_2" {
		t.Fatalf("unexpected tool_result order: %#v", resultContent)
	}
	if resultContent[2].(map[string]interface{})["text"] != "between" {
		t.Fatalf("expected deferred text after tool results, got %#v", resultContent)
	}
}

func TestOpenAI2ReqToClaudeReturnsInvalidToolChainErrorForOrphanResult(t *testing.T) {
	openai2Req := `{
		"model":"gpt-5.5",
		"stream":false,
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"lookup"}]},
			{"type":"function_call_output","call_id":"call_1","output":"ok"}
		]
	}`

	_, err := OpenAI2ReqToClaude([]byte(openai2Req), "claude-opus-4")
	if err == nil {
		t.Fatalf("expected InvalidToolChainError, got nil")
	}
	var toolErr *InvalidToolChainError
	if !errors.As(err, &toolErr) {
		t.Fatalf("expected InvalidToolChainError, got %T: %v", err, err)
	}
	if toolErr.CallID != "call_1" {
		t.Fatalf("expected call_id call_1, got %#v", toolErr.CallID)
	}
}

func TestClaudeReqToOpenAIReordersInterveningMessageAroundToolChain(t *testing.T) {
	claudeReq := `{
		"model":"claude-sonnet-4-20250514",
		"messages":[
			{"role":"user","content":"lookup"},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"path":"/tmp/a"}}]},
			{"role":"user","content":"between"},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}
		]
	}`

	reqBytes, err := ClaudeReqToOpenAI([]byte(claudeReq), "gpt-5.5")
	if err != nil {
		t.Fatalf("ClaudeReqToOpenAI failed: %v", err)
	}

	var req transformer.OpenAIRequest
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}

	if len(req.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d: %#v", len(req.Messages), req.Messages)
	}

	toolMsg := req.Messages[2]
	if toolMsg.Role != "tool" {
		t.Fatalf("expected tool message at index 2, got %#v", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "toolu_1" {
		t.Fatalf("expected tool_call_id toolu_1, got %#v", toolMsg.ToolCallID)
	}

	intervening := req.Messages[3]
	if intervening.Role != "user" {
		t.Fatalf("expected intervening user message after tool result, got %#v", intervening.Role)
	}
	if s, _ := intervening.Content.(string); s != "between" {
		t.Fatalf("expected intervening content 'between', got %#v", intervening.Content)
	}
}
