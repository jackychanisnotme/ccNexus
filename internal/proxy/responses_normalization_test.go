package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/transformer/cx/responses"
)

func TestNormalizeOpenAIResponsesToolSearchArgumentsJSON(t *testing.T) {
	tests := []struct {
		name        string
		payload     string
		wantChanged bool
		wantType    string
	}{
		{"tool search object string", `{"type":"tool_search_call","arguments":"{\"query\":\"logs\",\"limit\":8}"}`, true, "object"},
		{"tool search object", `{"type":"tool_search_call","arguments":{"query":"logs"}}`, false, "object"},
		{"tool search array string", `{"type":"tool_search_call","arguments":"[]"}`, false, "string"},
		{"tool search invalid string", `{"type":"tool_search_call","arguments":"{"}`, false, "string"},
		{"function call string", `{"type":"function_call","arguments":"{\"query\":\"logs\"}"}`, false, "string"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized, changed := normalizeOpenAIResponsesToolSearchArguments([]byte(tt.payload), false)
			if changed != tt.wantChanged {
				t.Fatalf("expected changed=%v, got %v payload=%s", tt.wantChanged, changed, normalized)
			}

			var item map[string]interface{}
			if err := json.Unmarshal(normalized, &item); err != nil {
				t.Fatalf("failed to decode normalized payload: %v", err)
			}
			if got := jsonValueType(item["arguments"]); got != tt.wantType {
				t.Fatalf("expected arguments type %q, got %q payload=%s", tt.wantType, got, normalized)
			}
		})
	}
}

func TestNormalizeOpenAIResponsesToolSearchArgumentsSSEOutputItem(t *testing.T) {
	payload := []byte("event: response.output_item.done\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"tool_search_call\",\"arguments\":\"{\\\"query\\\":\\\"logs\\\"}\"}}\n\n")

	normalized, changed := normalizeOpenAIResponsesToolSearchArguments(payload, true)
	if !changed {
		t.Fatal("expected SSE output item to change")
	}
	event := decodeSSEJSONForTest(t, normalized)
	item, ok := event["item"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected item object, got %#v", event["item"])
	}
	if got := jsonValueType(item["arguments"]); got != "object" {
		t.Fatalf("expected arguments object, got %q payload=%s", got, normalized)
	}
}

func TestNormalizeOpenAIResponsesToolSearchArgumentsSSECompleted(t *testing.T) {
	payload := []byte("data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"tool_search_call\",\"arguments\":\"{\\\"query\\\":\\\"logs\\\"}\"}]}}\n\n")

	normalized, changed := normalizeOpenAIResponsesToolSearchArguments(payload, true)
	if !changed {
		t.Fatal("expected completed SSE response to change")
	}
	event := decodeSSEJSONForTest(t, normalized)
	response, ok := event["response"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected response object, got %#v", event["response"])
	}
	output, ok := response["output"].([]interface{})
	if !ok || len(output) != 1 {
		t.Fatalf("expected one output item, got %#v", response["output"])
	}
	item, ok := output[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected output item object, got %#v", output[0])
	}
	if got := jsonValueType(item["arguments"]); got != "object" {
		t.Fatalf("expected arguments object, got %q payload=%s", got, normalized)
	}
}

func TestHandleNonStreamingResponseNormalizesToolSearchCallArguments(t *testing.T) {
	upstreamBody := `{"id":"resp-tool-search","object":"response","status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5},"output":[{"type":"tool_search_call","call_id":"call_search","status":"completed","arguments":"{\"query\":\"logs\",\"limit\":8}"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}
	rec := httptest.NewRecorder()
	p := &Proxy{}

	_, _, err := p.handleNonStreamingResponse(
		rec,
		resp,
		config.Endpoint{Name: "Primary", Transformer: "openai2", Model: "gpt-5.5"},
		responses.NewOpenAI2Transformer("gpt-5.5"),
	)
	if err != nil {
		t.Fatalf("handleNonStreamingResponse failed: %v", err)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode downstream response: %v", err)
	}
	output, ok := response["output"].([]interface{})
	if !ok || len(output) < 1 {
		t.Fatalf("expected output items, got %#v", response["output"])
	}
	assertToolSearchArgumentsObjectForTest(t, output[0])
}

func TestHandleStreamingResponseNormalizesToolSearchCallArguments(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-tool-search","object":"response","status":"in_progress","model":"gpt-5.5","output":[]}}`,
		"",
		`data: {"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"type":"tool_search_call","call_id":"call_search","status":"completed","arguments":"{\"query\":\"logs\",\"limit\":8}"}}`,
		"",
		`data: {"type":"response.output_text.delta","sequence_number":3,"output_index":1,"content_index":0,"item_id":"msg-ok","delta":"ok"}`,
		"",
		`data: {"type":"response.completed","sequence_number":4,"response":{"id":"resp-tool-search","object":"response","status":"completed","model":"gpt-5.5","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5},"output":[{"type":"tool_search_call","call_id":"call_search","status":"completed","arguments":"{\"query\":\"logs\",\"limit\":8}"},{"type":"message","id":"msg-ok","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}]}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstream)),
	}
	rec := httptest.NewRecorder()
	p := &Proxy{}
	streamSession := newDownstreamStreamSession(rec, 0, ClientFormatOpenAIResponses)

	result := p.handleStreamingResponse(
		httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true}`)).Context(),
		rec,
		resp,
		config.Endpoint{Name: "Primary", Transformer: "openai2", Model: "gpt-5.5"},
		responses.NewOpenAI2Transformer("gpt-5.5"),
		"cx_resp_openai2",
		false,
		"gpt-5.5",
		[]byte(`{"stream":true}`),
		0,
		streamSession,
	)
	if !result.Completed {
		t.Fatalf("expected completed stream, got reason=%q err=%v body=%q", result.Reason, result.Err, rec.Body.String())
	}

	events := decodeSSEEventsForTest(t, rec.Body.Bytes())
	foundOutputItem := false
	foundCompleted := false
	for _, event := range events {
		switch event["type"] {
		case "response.output_item.done":
			assertToolSearchArgumentsObjectForTest(t, event["item"])
			foundOutputItem = true
		case "response.completed":
			response, ok := event["response"].(map[string]interface{})
			if !ok {
				t.Fatalf("expected completed response object, got %#v", event["response"])
			}
			output, ok := response["output"].([]interface{})
			if !ok || len(output) < 1 {
				t.Fatalf("expected completed output items, got %#v", response["output"])
			}
			assertToolSearchArgumentsObjectForTest(t, output[0])
			foundCompleted = true
		}
	}
	if !foundOutputItem || !foundCompleted {
		t.Fatalf("expected output_item.done and response.completed events, got body=%q", rec.Body.String())
	}
}

func decodeSSEJSONForTest(t *testing.T, payload []byte) map[string]interface{} {
	t.Helper()
	for _, line := range strings.Split(string(payload), "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("failed to decode SSE data: %v", err)
		}
		return event
	}
	t.Fatal("SSE payload contained no JSON data line")
	return nil
}

func decodeSSEEventsForTest(t *testing.T, payload []byte) []map[string]interface{} {
	t.Helper()
	events := make([]map[string]interface{}, 0)
	for _, line := range strings.Split(string(payload), "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("failed to decode SSE data: %v", err)
		}
		events = append(events, event)
	}
	return events
}

func assertToolSearchArgumentsObjectForTest(t *testing.T, value interface{}) {
	t.Helper()
	item, ok := value.(map[string]interface{})
	if !ok {
		t.Fatalf("expected tool search item object, got %#v", value)
	}
	if item["type"] != "tool_search_call" {
		t.Fatalf("expected tool_search_call item, got %#v", item["type"])
	}
	if got := jsonValueType(item["arguments"]); got != "object" {
		t.Fatalf("expected arguments object, got %q item=%#v", got, item)
	}
}

func jsonValueType(value interface{}) string {
	switch value.(type) {
	case map[string]interface{}:
		return "object"
	case []interface{}:
		return "array"
	case string:
		return "string"
	case nil:
		return "null"
	default:
		return "scalar"
	}
}
