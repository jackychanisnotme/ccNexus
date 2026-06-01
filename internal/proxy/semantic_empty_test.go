package proxy

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestSemanticEmptyResponsesNonStreamingPositiveOutputTokensDoesNotRetry(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-empty","object":"response","status":"completed","usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18},"output":[]}`))
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
		t.Fatalf("expected positive-token empty response to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if hits != 1 {
		t.Fatalf("expected positive-token empty response not to retry, got hits=%d", hits)
	}
	if !strings.Contains(rec.Body.String(), "resp-empty") {
		t.Fatalf("expected original positive-token empty response to reach client, got %q", rec.Body.String())
	}
}

func TestSemanticEmptyResponsesNonStreamingZeroOutputTokensRetriesBeforeWriting(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		if hits == 1 {
			_, _ = w.Write([]byte(`{"id":"resp-empty","object":"response","status":"completed","usage":{"input_tokens":11,"output_tokens":0,"total_tokens":11},"output":[]}`))
			return
		}
		_, _ = w.Write([]byte(validResponsesBody("resp-ok", "ok")))
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
		t.Fatalf("expected retry to return final success, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if hits != 2 {
		t.Fatalf("expected zero-token empty response to be retried once, got hits=%d", hits)
	}
	if strings.Contains(rec.Body.String(), "resp-empty") || !strings.Contains(rec.Body.String(), "resp-ok") {
		t.Fatalf("expected only final non-empty response to reach client, got %q", rec.Body.String())
	}
}

func TestSemanticEmptyReasoningOnlyPositiveOutputTokensDoesNotRetry(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-reasoning","object":"response","status":"completed","usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18},"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking"}]}]}`))
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
		t.Fatalf("expected positive-token reasoning-only response to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if hits != 1 {
		t.Fatalf("expected positive-token reasoning-only response not to retry, got hits=%d", hits)
	}
}

func TestResponsesFunctionCallOnlyIsNotSemanticEmpty(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-tool","object":"response","status":"completed","usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7},"output":[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"codex\"}"}]}`))
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
		t.Fatalf("expected tool-call-only response to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if hits != 1 {
		t.Fatalf("expected no retry for valid function_call output, got hits=%d", hits)
	}
}

func TestResponsesToolLikeOutputOnlyIsNotSemanticEmpty(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-tool","object":"response","status":"completed","usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7},"output":[{"type":"custom_tool_call","id":"call_1","call_id":"call_1","name":"apply_patch","input":"patch"}]}`))
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
		t.Fatalf("expected custom_tool_call-only response to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if hits != 1 {
		t.Fatalf("expected no retry for custom_tool_call output, got hits=%d", hits)
	}
}

func TestOpenAIChatPositiveOutputTokensAndToolCallsAreValid(t *testing.T) {
	var emptyHits int
	emptyThenOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		emptyHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chat-empty","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}))
	defer emptyThenOK.Close()

	emptyEndpoint := failoverPolicyTestEndpoint("Primary", emptyThenOK.URL)
	emptyEndpoint.Transformer = "openai"
	p := newFailoverPolicyTestProxy([]config.Endpoint{emptyEndpoint}, emptyThenOK.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.5","stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected positive-token empty chat message to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if emptyHits != 1 {
		t.Fatalf("expected positive-token empty chat message not to retry, got hits=%d", emptyHits)
	}
	if !strings.Contains(rec.Body.String(), "chat-empty") {
		t.Fatalf("expected original positive-token empty chat response to reach client, got %q", rec.Body.String())
	}

	var toolHits int
	toolOnly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		toolHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chat-tool","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}))
	defer toolOnly.Close()

	toolEndpoint := failoverPolicyTestEndpoint("Primary", toolOnly.URL)
	toolEndpoint.Transformer = "openai"
	p = newFailoverPolicyTestProxy([]config.Endpoint{toolEndpoint}, toolOnly.Client())
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.5","stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected chat tool-call-only response to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if toolHits != 1 {
		t.Fatalf("expected no retry for chat tool_calls response, got hits=%d", toolHits)
	}
}

func TestClaudeEmptyMessagePositiveOutputTokensDoesNotRetry(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg-empty","type":"message","role":"assistant","content":[],"model":"claude-test","stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`))
	}))
	defer upstream.Close()

	endpoint := failoverPolicyTestEndpoint("Primary", upstream.URL)
	endpoint.Transformer = "claude"
	p := newFailoverPolicyTestProxy([]config.Endpoint{endpoint}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-test","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected positive-token empty Claude message to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if hits != 1 {
		t.Fatalf("expected positive-token empty Claude message not to retry, got hits=%d", hits)
	}
	if !strings.Contains(rec.Body.String(), "msg-empty") {
		t.Fatalf("expected original positive-token empty Claude response to reach client, got %q", rec.Body.String())
	}
}

func TestForceStreamAggregationSemanticEmptyRetriesBeforeWriting(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/event-stream")
		if hits == 1 {
			_, _ = w.Write([]byte(strings.Join([]string{
				`data: {"type":"response.completed","response":{"id":"resp-empty","object":"response","status":"completed","usage":{"input_tokens":2,"output_tokens":0,"total_tokens":2},"output":[]}}`,
				"",
				"data: [DONE]",
				"",
			}, "\n")))
			return
		}
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.completed","response":{"id":"resp-ok","object":"response","status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	endpoint := failoverPolicyTestEndpoint("Primary", upstream.URL)
	endpoint.ForceStream = true
	p := newFailoverPolicyTestProxy([]config.Endpoint{endpoint}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected forced stream aggregation retry to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if hits != 2 {
		t.Fatalf("expected aggregate empty response to be retried once, got hits=%d", hits)
	}
	if strings.Contains(rec.Body.String(), "resp-empty") || !strings.Contains(rec.Body.String(), "resp-ok") {
		t.Fatalf("expected only final aggregated response to reach client, got %q", rec.Body.String())
	}
}

func TestStreamingSemanticEmptyRetriesAfterDownstreamHeartbeat(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/event-stream")
		if hits == 1 {
			_, _ = w.Write([]byte(strings.Join([]string{
				`data: {"type":"response.created","response":{"id":"resp-empty","object":"response","status":"in_progress"}}`,
				"",
				`data: {"type":"response.completed","response":{"id":"resp-empty","object":"response","status":"completed","usage":{"input_tokens":2,"output_tokens":0,"total_tokens":2},"output":[]}}`,
				"",
				"data: [DONE]",
				"",
			}, "\n")))
			return
		}
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"ok"}`,
			"",
			`data: {"type":"response.completed","response":{"id":"resp-ok","object":"response","status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected streaming retry to keep response open and succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if hits != 2 {
		t.Fatalf("expected empty stream to be retried once, got hits=%d", hits)
	}
	body := rec.Body.String()
	if !strings.Contains(body, ": ccnexus waiting for upstream") || !strings.Contains(body, "response.output_text.delta") {
		t.Fatalf("expected downstream heartbeat and final semantic event, got %q", body)
	}
	if strings.Contains(body, "resp-empty") {
		t.Fatalf("did not expect first empty stream events to be forwarded, got %q", body)
	}
}

func TestStreamingPositiveOutputTokenResponsesEmptyCompletesWithoutRetry(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/event-stream")
		if hits == 1 {
			_, _ = w.Write([]byte(strings.Join([]string{
				`data: {"type":"response.created","response":{"id":"resp-empty","object":"response","status":"in_progress"}}`,
				"",
				`data: {"type":"response.completed","response":{"id":"resp-empty","object":"response","status":"completed","usage":{"input_tokens":2,"output_tokens":386,"total_tokens":388},"output":[]}}`,
				"",
				"data: [DONE]",
				"",
			}, "\n")))
			return
		}
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"unexpected retry"}`,
			"",
			`data: {"type":"response.completed","response":{"id":"resp-retry","object":"response","status":"completed","usage":{"input_tokens":2,"output_tokens":2,"total_tokens":4},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"unexpected retry"}]}]}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected positive-token empty stream to complete, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if hits != 1 {
		t.Fatalf("expected no semantic-empty retry for positive-token empty stream, got hits=%d", hits)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "resp-empty") || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("expected original terminal empty stream to reach client, got %q", body)
	}
	if strings.Contains(joinedProxyLogs(), retryReasonSemanticEmptyResponse) {
		t.Fatalf("did not expect semantic-empty retry log, got logs:\n%s", joinedProxyLogs())
	}
}

func TestStreamingZeroTokenResponsesEmptyStillRetries(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/event-stream")
		if hits == 1 {
			_, _ = w.Write([]byte(strings.Join([]string{
				`data: {"type":"response.completed","response":{"id":"resp-empty","object":"response","status":"completed","usage":{"input_tokens":2,"output_tokens":0,"total_tokens":2},"output":[]}}`,
				"",
				"data: [DONE]",
				"",
			}, "\n")))
			return
		}
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"ok"}`,
			"",
			`data: {"type":"response.completed","response":{"id":"resp-ok","object":"response","status":"completed","usage":{"input_tokens":2,"output_tokens":2,"total_tokens":4},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected zero-token empty stream retry to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if hits != 2 {
		t.Fatalf("expected zero-token empty stream to be retried, got hits=%d", hits)
	}
	if !strings.Contains(rec.Body.String(), `"delta":"ok"`) {
		t.Fatalf("expected fallback stream output, got %q", rec.Body.String())
	}
}

func TestStreamingTextDeltaWithCompletedOutputIsNotSemanticEmpty(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-ok","object":"response","status":"in_progress","output":[]}}`,
			"",
			`data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"type":"message","id":"msg_resp-ok_0","role":"assistant","status":"in_progress","content":[]}}`,
			"",
			`data: {"type":"response.content_part.added","sequence_number":3,"output_index":0,"content_index":0,"item_id":"msg_resp-ok_0","part":{"type":"output_text","text":""}}`,
			"",
			`data: {"type":"response.output_text.delta","sequence_number":4,"output_index":0,"content_index":0,"item_id":"msg_resp-ok_0","logprobs":[],"delta":"ok"}`,
			"",
			`data: {"type":"response.completed","sequence_number":5,"response":{"id":"resp-ok","object":"response","status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5},"output":[{"type":"message","id":"msg_resp-ok_0","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}]}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected streaming response to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if hits != 1 {
		t.Fatalf("expected valid stream not to retry as semantic empty, got hits=%d", hits)
	}
	if !strings.Contains(rec.Body.String(), `"text":"ok"`) {
		t.Fatalf("expected completed output text to reach client, got %q", rec.Body.String())
	}
}

func TestStreamingOpenAIChatCompletedOnlyOutputIsNotSemanticEmpty(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.completed","response":{"id":"resp-ok","object":"response","status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.5","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected streaming response to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if hits != 1 {
		t.Fatalf("expected valid completed-only stream not to retry as semantic empty, got hits=%d", hits)
	}
	if !strings.Contains(rec.Body.String(), `"content":"ok"`) {
		t.Fatalf("expected completed output text to be converted to chat content, got %q", rec.Body.String())
	}
}

func TestSemanticStreamOutputTextDoneIsOutput(t *testing.T) {
	inspection := inspectSemanticStreamEvent([]byte(`data: {"type":"response.output_text.done","text":"ok"}`))
	if !inspection.HasOutput {
		t.Fatalf("expected output_text.done text to count as semantic output")
	}
}

func TestSemanticStreamCustomToolCallIsOutput(t *testing.T) {
	inspection := inspectSemanticStreamEvent([]byte(`data: {"type":"response.output_item.done","item":{"type":"custom_tool_call","id":"call_1","call_id":"call_1","name":"apply_patch","input":"patch"}}`))
	if !inspection.HasOutput {
		t.Fatalf("expected custom_tool_call item to count as semantic output")
	}

	inspection = inspectSemanticStreamEvent([]byte(`data: {"type":"response.custom_tool_call_input.delta","delta":"patch"}`))
	if !inspection.HasOutput {
		t.Fatalf("expected custom_tool_call_input delta to count as semantic output")
	}
}

func TestTokenPoolSemanticEmptySoftCoolsCredentialAndRetriesNextToken(t *testing.T) {
	var tokens []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		tokens = append(tokens, token)
		w.Header().Set("Content-Type", "application/json")
		if token == "token-a" {
			_, _ = w.Write([]byte(`{"id":"resp-empty","object":"response","status":"completed","usage":{"input_tokens":11,"output_tokens":0,"total_tokens":11},"output":[]}`))
			return
		}
		_, _ = w.Write([]byte(validResponsesBody("resp-ok", "ok")))
	}))
	defer upstream.Close()

	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ccnexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	credA := storage.EndpointCredential{EndpointName: "Primary", ProviderType: "openai", AccessToken: "token-a", Enabled: true}
	credB := storage.EndpointCredential{EndpointName: "Primary", ProviderType: "openai", AccessToken: "token-b", Enabled: true}
	if err := store.SaveEndpointCredential(&credA); err != nil {
		t.Fatalf("save cred A: %v", err)
	}
	if err := store.SaveEndpointCredential(&credB); err != nil {
		t.Fatalf("save cred B: %v", err)
	}

	cfg := config.DefaultConfig()
	endpoint := failoverPolicyTestEndpoint("Primary", upstream.URL)
	endpoint.AuthMode = config.AuthModeTokenPool
	endpoint.APIKey = ""
	cfg.UpdateEndpoints([]config.Endpoint{endpoint})
	p := New(cfg, &noopStatsStorage{}, store, "test-device")
	p.httpClient = upstream.Client()
	p.retrySleep = func(time.Duration) {}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected token pool retry to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if strings.Join(tokens, ",") != "token-a,token-b" {
		t.Fatalf("expected retry to move from token-a to token-b, got tokens=%v", tokens)
	}

	updatedA, err := store.GetCredentialByID(credA.ID)
	if err != nil {
		t.Fatalf("load cred A: %v", err)
	}
	if updatedA == nil || updatedA.Status != "cooldown" || updatedA.CooldownUntil == nil {
		t.Fatalf("expected token-a to be soft-cooled, got %#v", updatedA)
	}
	if strings.Contains(strings.ToLower(updatedA.LastError), "invalid") {
		t.Fatalf("expected semantic empty not to invalidate token, got last_error=%q", updatedA.LastError)
	}
}

func validResponsesBody(id, text string) string {
	return `{"id":"` + id + `","object":"response","status":"completed","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + text + `"}]}]}`
}

func TestStreamingClaudeKeepAliveUsesPingEvent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		// Non-semantic events first; ccNexus buffers these and must keep the
		// client alive with its own ping heartbeat until a semantic event lands.
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(60 * time.Millisecond)
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"))
		_, _ = w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	endpoint := failoverPolicyTestEndpoint("Primary", upstream.URL)
	endpoint.Transformer = "claude"
	p := newFailoverPolicyTestProxy([]config.Endpoint{endpoint}, upstream.Client())
	p.streamHeartbeatInterval = 10 * time.Millisecond

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-test","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected Claude stream to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: ping") || !strings.Contains(body, "\"type\": \"ping\"") {
		t.Fatalf("expected Anthropic ping keep-alive in Claude stream, got %q", body)
	}
	if strings.Contains(body, ": ccnexus waiting for upstream\n\n: ccnexus waiting for upstream") {
		t.Fatalf("did not expect repeated comment heartbeat on Claude stream, got %q", body)
	}
	if !strings.Contains(body, "ok") {
		t.Fatalf("expected eventual semantic text to reach client, got %q", body)
	}
}

func TestStreamingClaudeReasoningFlushedBeforeText(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":5}}}`,
			"",
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			"",
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"pondering"}}`,
			"",
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			"",
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
			"",
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}`,
			"",
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":1}`,
			"",
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`,
			"",
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	endpoint := failoverPolicyTestEndpoint("Primary", upstream.URL)
	endpoint.Transformer = "claude"
	p := newFailoverPolicyTestProxy([]config.Endpoint{endpoint}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected reasoning+text stream to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	reasoningAt := strings.Index(body, "response.reasoning_text.delta")
	textAt := strings.Index(body, "response.output_text.delta")
	if reasoningAt < 0 || textAt < 0 {
		t.Fatalf("expected both reasoning and text events, got %q", body)
	}
	if reasoningAt > textAt {
		t.Fatalf("expected reasoning to be emitted before text, got %q", body)
	}
	if !strings.Contains(body, `"delta":"pondering"`) || !strings.Contains(body, `"delta":"answer"`) {
		t.Fatalf("expected reasoning and text deltas in body, got %q", body)
	}
}

func TestStreamingClaudeReasoningOnlyEmptyClosesWithoutRetry(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":5}}}`,
			"",
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			"",
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"only thinking"}}`,
			"",
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			"",
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":0}}`,
			"",
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	endpoint := failoverPolicyTestEndpoint("Primary", upstream.URL)
	endpoint.Transformer = "claude"
	p := newFailoverPolicyTestProxy([]config.Endpoint{endpoint}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if hits != 1 {
		t.Fatalf("expected no silent retry after reasoning was streamed, got hits=%d", hits)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "response.reasoning_text.delta") {
		t.Fatalf("expected reasoning to be streamed to client, got %q", body)
	}
	if !strings.Contains(body, "event: error") {
		t.Fatalf("expected stream to close with an error event, got %q", body)
	}
}
