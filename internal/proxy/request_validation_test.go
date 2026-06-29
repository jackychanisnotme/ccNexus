package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
)

func TestHandleProxyHeadRootIsLightweightProbe(t *testing.T) {
	upstreamHits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-upstream","usage":{"input_tokens":1,"output_tokens":1},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())

	req := httptest.NewRequest(http.MethodHead, "/", nil)
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected HEAD / probe status 204, got %d body=%q", rec.Code, rec.Body.String())
	}
	if upstreamHits != 0 {
		t.Fatalf("expected HEAD / probe to skip upstream endpoints, got hits=%d", upstreamHits)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("expected HEAD / probe to return no body, got %q", rec.Body.String())
	}
}

func TestHandleProxyRejectsInvalidJSONBeforeEndpointAttempt(t *testing.T) {
	upstreamHits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-upstream","usage":{"input_tokens":1,"output_tokens":1},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())

	tests := []struct {
		name string
		body string
	}{
		{name: "empty body", body: ""},
		{name: "malformed json", body: `{"model":"gpt-5.5"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstreamHits = 0
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			p.handleProxy(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d body=%q", rec.Code, rec.Body.String())
			}
			if upstreamHits != 0 {
				t.Fatalf("expected invalid request to skip upstream endpoints, got hits=%d", upstreamHits)
			}
			if !strings.Contains(rec.Body.String(), "invalid_request_error") {
				t.Fatalf("expected structured invalid request response, got %q", rec.Body.String())
			}
		})
	}
}

func TestHandleProxyRejectsResponsesRequestMissingInputBeforeEndpointAttempt(t *testing.T) {
	upstreamHits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-upstream","usage":{"input_tokens":1,"output_tokens":1},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%q", rec.Code, rec.Body.String())
	}
	if upstreamHits != 0 {
		t.Fatalf("expected missing input to skip upstream endpoints, got hits=%d", upstreamHits)
	}
	if !strings.Contains(rec.Body.String(), "field input is required") {
		t.Fatalf("expected missing input error, got %q", rec.Body.String())
	}
}

func TestHandleProxyNormalizesToolRoleInResponsesInputForOpenAI2Upstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode upstream request: %v", err)
		}

		input, ok := body["input"].([]interface{})
		if !ok {
			t.Fatalf("expected input array, got %#v", body["input"])
		}
		for _, rawItem := range input {
			item, ok := rawItem.(map[string]interface{})
			if !ok {
				continue
			}
			if item["role"] == "tool" {
				t.Fatalf("did not expect role=tool to reach upstream: %#v", item)
			}
		}

		toolOutput := input[1].(map[string]interface{})
		if toolOutput["type"] != "function_call_output" {
			t.Fatalf("expected function_call_output, got %#v", toolOutput)
		}
		if toolOutput["call_id"] != "call_1" {
			t.Fatalf("expected call_id=call_1, got %#v", toolOutput["call_id"])
		}
		if toolOutput["output"] != "牧原股份基本面数据" {
			t.Fatalf("expected normalized tool output text, got %#v", toolOutput["output"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-upstream","usage":{"input_tokens":1,"output_tokens":1},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())

	body := `{
		"model":"gpt-5.5",
		"stream":false,
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"lookup"}]},
			{"type":"message","role":"tool","tool_call_id":"call_1","content":"牧原股份基本面数据"}
		]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected upstream success, got status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestHandleProxyAutoForceStreamsOpenAI2WhenUpstreamRequiresStream(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode upstream request: %v", err)
		}

		stream, _ := body["stream"].(bool)
		if upstreamHits == 1 {
			if stream {
				t.Fatalf("first upstream attempt should preserve non-stream client request, got stream=true")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"400: {\"detail\":\"Stream must be set to true\"}","type":"bad_response_status_code","code":"bad_response_status_code"}}`))
			return
		}
		if !stream {
			t.Fatalf("second upstream attempt should force stream=true, got body=%#v", body)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.completed","response":{"id":"resp-stream","object":"response","status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected forced streaming retry to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if upstreamHits != 2 {
		t.Fatalf("expected one compatibility retry, got upstream hits=%d", upstreamHits)
	}
	if !strings.Contains(rec.Body.String(), `"id":"resp-stream"`) {
		t.Fatalf("expected aggregated Responses payload, got %q", rec.Body.String())
	}
}

func TestHandleProxyTreatsWrappedInvalidRequest500AsClientError(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"field messages is required","type":"new_api_error","code":"invalid_request"}}`))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected wrapped invalid_request to be returned as 400, got %d body=%q", rec.Code, rec.Body.String())
	}
	if upstreamHits != 1 {
		t.Fatalf("expected wrapped invalid_request not to retry, got hits=%d", upstreamHits)
	}
	if !strings.Contains(rec.Body.String(), "invalid_request") {
		t.Fatalf("expected upstream invalid request body, got %q", rec.Body.String())
	}
	p.cooldownMu.RLock()
	cooldowns := len(p.endpointCooldowns)
	p.cooldownMu.RUnlock()
	if cooldowns != 0 {
		t.Fatalf("expected no endpoint cooldown for client invalid request, got %d", cooldowns)
	}
}

func TestHandleProxyRetriesOpenAI2WhenFunctionCallArgumentsMustBeObjects(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode upstream request: %v", err)
		}
		input, ok := body["input"].([]interface{})
		if !ok {
			t.Fatalf("expected input array, got %#v", body["input"])
		}
		functionCall := input[1].(map[string]interface{})
		if upstreamHits == 1 {
			if _, ok := functionCall["arguments"].(string); !ok {
				t.Fatalf("first attempt should preserve standard string arguments, got %#v", functionCall["arguments"])
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid type for 'input[1].arguments': expected an object, but got a string instead.","type":"invalid_request_error","param":"input[1].arguments","code":"invalid_type"}}`))
			return
		}
		arguments, ok := functionCall["arguments"].(map[string]interface{})
		if !ok {
			t.Fatalf("compat retry should send object arguments, got %#v", functionCall["arguments"])
		}
		if arguments["symbol"] != "002714" {
			t.Fatalf("expected parsed symbol argument, got %#v", arguments)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-upstream","usage":{"input_tokens":1,"output_tokens":1},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())

	body := `{
		"model":"gpt-5.5",
		"stream":false,
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"lookup"}]},
			{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"symbol\":\"002714\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"ok"}
		]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected compatibility retry to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if upstreamHits != 2 {
		t.Fatalf("expected one compatibility retry, got upstream hits=%d", upstreamHits)
	}
	if !strings.Contains(rec.Body.String(), `"id":"resp-upstream"`) {
		t.Fatalf("expected upstream response body, got %q", rec.Body.String())
	}
}

func TestHandleProxyOnlyConvertsRejectedOpenAI2ArgumentIndexToObject(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode upstream request: %v", err)
		}
		input, ok := body["input"].([]interface{})
		if !ok {
			t.Fatalf("expected input array, got %#v", body["input"])
		}
		rejectedCall := input[8].(map[string]interface{})
		standardCall := input[12].(map[string]interface{})
		switch upstreamHits {
		case 1:
			if _, ok := rejectedCall["arguments"].(string); !ok {
				t.Fatalf("first attempt should preserve rejected-call string arguments, got %#v", rejectedCall["arguments"])
			}
			if _, ok := standardCall["arguments"].(string); !ok {
				t.Fatalf("first attempt should preserve standard-call string arguments, got %#v", standardCall["arguments"])
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid type for 'input[8].arguments': expected an object, but got a string instead.","type":"invalid_request_error","param":"input[8].arguments","code":"invalid_type"}}`))
			return
		case 2:
			arguments, ok := rejectedCall["arguments"].(map[string]interface{})
			if !ok {
				t.Fatalf("compat retry should send object arguments for rejected index, got %#v", rejectedCall["arguments"])
			}
			if arguments["symbol"] != "002714" {
				t.Fatalf("expected parsed symbol argument, got %#v", arguments)
			}
			if _, ok := standardCall["arguments"].(string); !ok {
				t.Fatalf("compat retry should preserve unrelated string arguments, got %#v", standardCall["arguments"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"resp-upstream","usage":{"input_tokens":1,"output_tokens":1},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
			return
		default:
			t.Fatalf("unexpected extra upstream hit %d", upstreamHits)
		}
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())

	body := `{
		"model":"gpt-5.5",
		"stream":false,
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"lookup"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"thinking"}]},
			{"type":"function_call_output","call_id":"call_0","output":"ok"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"more"}]},
			{"type":"function_call_output","call_id":"call_x","output":"ok"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"lookup"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"tool next"}]},
			{"type":"function_call","call_id":"call_rejected","name":"lookup","arguments":"{\"symbol\":\"002714\"}"},
			{"type":"function_call_output","call_id":"call_rejected","output":"ok"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"second lookup"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"tool next"}]},
			{"type":"function_call","call_id":"call_standard","name":"lookup","arguments":"{\"symbol\":\"000001\"}"}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected compatibility retry to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if upstreamHits != 2 {
		t.Fatalf("expected one compatibility retry, got upstream hits=%d", upstreamHits)
	}
}

func TestHandleProxyDoesNotRetryOpenAI2ArgumentsObjectCompatForUnrelatedBadRequest(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad request","type":"invalid_request_error"}}`))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected upstream bad request to be returned, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if upstreamHits != 1 {
		t.Fatalf("did not expect compatibility retry for unrelated 400, got upstream hits=%d", upstreamHits)
	}
}
