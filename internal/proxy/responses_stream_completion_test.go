package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/transformer/cx/responses"
)

type cancelAfterReadCloser struct {
	reader   *strings.Reader
	cancel   context.CancelFunc
	canceled bool
}

func (c *cancelAfterReadCloser) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	if n > 0 && !c.canceled {
		c.canceled = true
		c.cancel()
	}
	return n, err
}

func (c *cancelAfterReadCloser) Close() error {
	return nil
}

func TestResponsesStreamMissingCompletedBeforeDoneIsToleratedForCodexClient(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-primary","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
			"",
			`data: {"type":"response.output_text.delta","sequence_number":2,"output_index":0,"content_index":0,"item_id":"msg-primary","delta":"hello"}`,
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
	req.Header.Set("User-Agent", "Codex_Desktop/0.133.0")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected stream response to stay HTTP 200, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "response.output_text.delta") || !strings.Contains(body, `"delta":"hello"`) {
		t.Fatalf("expected streamed text delta to reach tolerant Codex client, got %q", body)
	}
	if strings.Contains(body, "response.completed") {
		t.Fatalf("did not expect synthetic response.completed for tolerant Codex client, got %q", body)
	}
	if strings.Contains(body, "event: error") || strings.Contains(body, "message_delta") {
		t.Fatalf("did not expect error or Claude message_delta in tolerant Codex stream, got %q", body)
	}
}

func TestResponsesStreamMissingCompletedAtEOFIsToleratedForCodexClient(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-eof","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
			"",
			`data: {"type":"response.output_text.delta","sequence_number":2,"output_index":0,"content_index":0,"item_id":"msg-eof","delta":"ok"}`,
			"",
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Codex_Desktop/0.133.0")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "response.output_text.delta") || !strings.Contains(body, `"delta":"ok"`) {
		t.Fatalf("expected streamed text delta to reach tolerant Codex client, got %q", body)
	}
	if strings.Contains(body, "response.completed") || strings.Contains(body, "data: [DONE]") || strings.Contains(body, "event: error") {
		t.Fatalf("did not expect synthetic completion, invented [DONE], or error for tolerant Codex client, got %q", body)
	}
}

func TestResponsesStreamMissingCompletedAtEOFCompletesOpenAIPythonClient(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-python","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
			"",
			`data: {"type":"response.output_text.delta","sequence_number":2,"output_index":0,"content_index":0,"item_id":"msg-python","delta":"hello hermes"}`,
			"",
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OpenAI/Python_2.31.0")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "response.completed") || !strings.Contains(body, `"text":"hello hermes"`) {
		t.Fatalf("expected synthetic response.completed for Hermes/Python client, got %q", body)
	}
	if strings.Contains(body, "event: error") || strings.Contains(body, "missing_response_completed") {
		t.Fatalf("did not expect error after safe synthetic completion, got %q", body)
	}
}

func TestResponsesStreamMissingCompletedFromMessageOutputItemCompletesOpenAIPythonClient(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-python-item","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
			"",
			`data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"type":"message","id":"msg-python-item","role":"assistant","status":"completed","content":[{"type":"output_text","text":"item text"}]}}`,
			"",
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OpenAI/Python_2.31.0")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "response.completed") || !strings.Contains(body, `"text":"item text"`) {
		t.Fatalf("expected synthetic response.completed from message output item, got %q", body)
	}
	if strings.Contains(body, "event: error") || strings.Contains(body, "missing_response_completed") {
		t.Fatalf("did not expect error after message output item completion, got %q", body)
	}
}

func TestResponsesStreamMessageOutputItemSyntheticCompletionTracksOutputText(t *testing.T) {
	p := &Proxy{}
	endpoint := config.Endpoint{Name: "Primary", Transformer: "openai2", Model: "gpt-5.5"}
	upstream := strings.Join([]string{
		`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-item-text","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
		"",
		`data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"type":"message","id":"msg-item-text","role":"assistant","status":"completed","content":[{"type":"output_text","text":"item text"}]}}`,
		"",
		"",
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstream)),
	}
	rec := httptest.NewRecorder()
	streamSession := newDownstreamStreamSession(rec, 0, ClientFormatOpenAIResponses)

	result := p.handleStreamingResponse(
		httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true}`)).Context(),
		rec,
		resp,
		endpoint,
		responses.NewOpenAI2Transformer("gpt-5.5"),
		"cx_resp_openai2",
		false,
		"gpt-5.5",
		[]byte(`{"stream":true}`),
		0,
		streamSession,
	)

	if !result.Completed {
		t.Fatalf("expected synthetic completion to succeed, got reason=%q err=%v body=%q", result.Reason, result.Err, rec.Body.String())
	}
	if result.OutputText != "item text" {
		t.Fatalf("expected output text to be tracked from message output item, got %q", result.OutputText)
	}
	if result.OutputTokens <= 0 {
		t.Fatalf("expected output tokens to be estimated from message output item, got %d", result.OutputTokens)
	}
}

func TestResponsesStreamMissingCompletedWithNoOutputDoesNotSucceed(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-empty","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
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
	req.Header.Set("User-Agent", "Codex_Desktop/0.133.0")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if hits < 2 {
		t.Fatalf("expected empty incomplete stream to be retried before final failure, got hits=%d", hits)
	}
	body := rec.Body.String()
	if strings.Contains(body, `"type":"response.completed"`) {
		t.Fatalf("did not expect synthetic response.completed for empty incomplete stream, got %q", body)
	}
	if !strings.Contains(body, "event: error") {
		t.Fatalf("expected final stream error for empty incomplete stream, got %q", body)
	}
}

func TestResponsesStreamMissingCompletedAfterOutputIsToleratedForCodexClient(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	var fallbackHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-primary","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
			"",
			`data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"type":"message","id":"msg-primary","role":"assistant","status":"in_progress","content":[{"type":"output_text","text":"partial"}]}}`,
			"",
			"",
		}, "\n")))
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.output_text.delta","sequence_number":1,"output_index":0,"content_index":0,"item_id":"msg-fallback","delta":"fallback"}`,
			"",
			`data: {"type":"response.completed","sequence_number":2,"response":{"id":"resp-fallback","object":"response","status":"completed","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3},"output":[{"type":"message","id":"msg-fallback","role":"assistant","status":"completed","content":[{"type":"output_text","text":"fallback"}]}]}}`,
			"",
		}, "\n")))
	}))
	defer fallback.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", primary.URL),
		failoverPolicyTestEndpoint("Fallback", fallback.URL),
	}, primary.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Codex_Desktop/0.142.0-alpha.1")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if primaryHits != 1 {
		t.Fatalf("expected Primary to be used once, got %d", primaryHits)
	}
	if fallbackHits != 0 {
		t.Fatalf("expected tolerant Codex client not to fallback, got fallback hits=%d", fallbackHits)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "response.output_item.added") || !strings.Contains(body, "partial") {
		t.Fatalf("expected original partial output to reach Codex client, got %q", body)
	}
	for _, notWant := range []string{
		"event: error",
		"missing_response_completed",
		"fallback",
	} {
		if strings.Contains(body, notWant) {
			t.Fatalf("did not expect downstream body to contain %q, got %q", notWant, body)
		}
	}
	p.cooldownMu.RLock()
	_, cooled := p.endpointCooldowns["Primary"]
	p.cooldownMu.RUnlock()
	if cooled {
		t.Fatal("expected missing completed tolerated for Codex not to cool Primary")
	}
	logs := joinedProxyLogs()
	if !strings.Contains(logs, "Tolerating missing response.completed for tolerant client") {
		t.Fatalf("expected tolerant missing completed log, got logs:\n%s", logs)
	}
}

func TestResponsesStreamMissingCompletedWinsOverCanceledContextForCodexClient(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	ctx, cancel := context.WithCancel(context.Background())
	var primaryHits int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		primaryHits++
		body := strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-primary","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
			"",
			`data: {"type":"response.output_text.delta","sequence_number":2,"output_index":0,"content_index":0,"item_id":"msg-primary","delta":"partial"}`,
			"",
			"",
		}, "\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       &cancelAfterReadCloser{reader: strings.NewReader(body), cancel: cancel},
			Request:    req,
		}, nil
	})}
	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("备用，香港2", "https://backup.example/v1/responses"),
	}, client)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Codex_Desktop/0.142.0-alpha.1")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if primaryHits != 1 {
		t.Fatalf("expected primary to be used once, got %d", primaryHits)
	}
	body := rec.Body.String()
	if strings.Contains(body, "event: error") || strings.Contains(body, "fallback") {
		t.Fatalf("did not expect error or fallback for tolerant Codex client, got %q", body)
	}
	logs := joinedProxyLogs()
	if !strings.Contains(logs, "Tolerating missing response.completed for tolerant client") {
		t.Fatalf("expected missing response.completed tolerance log, got logs:\n%s", logs)
	}
	if strings.Contains(logs, "Client canceled streaming response") {
		t.Fatalf("expected missing response.completed not to be misclassified as client canceled, got logs:\n%s", logs)
	}
}

func TestResponsesStreamMissingCompletedEmptyPythonCompatibleEndpointSoftFallbacks(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	var fallbackHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-primary","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
			"",
			`data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"type":"message","id":"msg-primary","role":"assistant","status":"in_progress","content":[]}}`,
			"",
			"",
		}, "\n")))
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-fallback","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
			"",
			`data: {"type":"response.output_text.delta","sequence_number":2,"output_index":0,"content_index":0,"item_id":"msg-fallback","delta":"fallback text"}`,
			"",
			`data: {"type":"response.completed","sequence_number":3,"response":{"id":"resp-fallback","object":"response","status":"completed","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3},"output":[{"type":"message","id":"msg-fallback","role":"assistant","status":"completed","content":[{"type":"output_text","text":"fallback text"}]}]}}`,
			"",
		}, "\n")))
	}))
	defer fallback.Close()

	primaryEndpoint := failoverPolicyTestEndpoint("1052-1st", primary.URL)
	p := newFailoverPolicyTestProxy([]config.Endpoint{
		primaryEndpoint,
		failoverPolicyTestEndpoint("Fallback", fallback.URL),
	}, primary.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OpenAI/Python_2.31.0")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if primaryHits != 1 {
		t.Fatalf("expected compatible primary to be tried once, got %d", primaryHits)
	}
	if fallbackHits != 1 {
		t.Fatalf("expected compatible empty Python stream to soft fallback once, got %d", fallbackHits)
	}
	body := rec.Body.String()
	if strings.Contains(body, "msg-primary") {
		t.Fatalf("did not expect empty primary structural event to be committed downstream, got %q", body)
	}
	if !strings.Contains(body, "response.completed") || !strings.Contains(body, "fallback text") {
		t.Fatalf("expected fallback completed response, got %q", body)
	}
	p.cooldownMu.RLock()
	_, cooled := p.endpointCooldowns["1052-1st"]
	p.cooldownMu.RUnlock()
	if cooled {
		t.Fatal("expected compatible missing completed soft fallback not to cool 1052-1st")
	}
	logs := joinedProxyLogs()
	for _, want := range []string{
		"Soft-fallback missing response.completed for compatible endpoint",
		"responses_text_len=0",
		"last_transformed_event_type=response.output_item.added",
		"last_output_item_type=message",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %q; logs:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "[CIRCUIT_BREAKER]") {
		t.Fatalf("did not expect compatible soft fallback to trigger circuit breaker; logs:\n%s", logs)
	}
}

func TestResponsesStreamMissingCompletedEmptyPythonNonCompatibleEndpointStaysStrict(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-primary","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
			"",
			`data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"type":"message","id":"msg-primary","role":"assistant","status":"in_progress","content":[]}}`,
			"",
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", upstream.URL),
	}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OpenAI/Python_2.31.0")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, `"type":"response.completed"`) {
		t.Fatalf("did not expect synthetic completion for empty non-compatible endpoint, got %q", body)
	}
	if !strings.Contains(body, "event: error") {
		t.Fatalf("expected strict missing completed error for non-compatible endpoint, got %q", body)
	}
}

func TestResponsesStreamMissingCompletedForToolCallWritesError(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-tool","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
			"",
			`data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"call-1","type":"function_call","name":"edit_file","arguments":"{}"}}`,
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
	req.Header.Set("User-Agent", "OpenAI/Python_2.31.0")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, `"type":"response.completed"`) {
		t.Fatalf("did not expect synthetic response.completed for incomplete tool stream, got %q", body)
	}
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "missing_response_completed") {
		t.Fatalf("expected explicit missing_response_completed stream error, got %q", body)
	}

	logs := joinedProxyLogs()
	for _, want := range []string{
		"wrote_data=true",
		"wrote_semantic_data=true",
		"first_transformed_event_type=",
		"synthetic_completion_attempted=false",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected missing response.completed logs to contain %q; logs:\n%s", want, logs)
		}
	}
}

func TestResponsesStreamMissingCompletedWithDoneFunctionCallCompletesOpenAIPythonClient(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-tool-done","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
			"",
			`data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"call-1","call_id":"call-1","type":"function_call","name":"edit_file","arguments":"{}","status":"in_progress"}}`,
			"",
			`data: {"type":"response.function_call_arguments.done","sequence_number":3,"item_id":"call-1","output_index":0,"arguments":"{\"path\":\"a.go\"}"}`,
			"",
			`data: {"type":"response.output_item.done","sequence_number":4,"output_index":0,"item":{"id":"call-1","call_id":"call-1","type":"function_call","name":"edit_file","arguments":"{\"path\":\"a.go\"}","status":"completed"}}`,
			"",
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("1052-1st", upstream.URL),
	}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OpenAI/Python_2.31.0")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `"type":"response.completed"`) {
		t.Fatalf("expected synthetic response.completed for completed function_call, got %q", body)
	}
	if !strings.Contains(body, `"type":"function_call"`) || !strings.Contains(body, `"name":"edit_file"`) || !strings.Contains(body, `"arguments":"{\"path\":\"a.go\"}"`) {
		t.Fatalf("expected synthetic completion to preserve function_call output item, got %q", body)
	}
	if strings.Contains(body, "event: error") || strings.Contains(body, "missing_response_completed") {
		t.Fatalf("did not expect error after completed function_call recovery, got %q", body)
	}
	p.cooldownMu.RLock()
	_, cooled := p.endpointCooldowns["1052-1st"]
	p.cooldownMu.RUnlock()
	if cooled {
		t.Fatal("expected completed function_call recovery not to cool 1052-1st")
	}
	logs := joinedProxyLogs()
	for _, want := range []string{
		"Completing OpenAI Responses function_call stream missing response.completed",
		"responses_tool_recoverable=true",
		"responses_tool_pending=false",
		"responses_output_items=1",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %q; logs:\n%s", want, logs)
		}
	}
}

func TestResponsesStreamMissingCompletedWithOnlyFunctionCallAddedStaysStrict(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-tool-added","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
			"",
			`data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"call-1","call_id":"call-1","type":"function_call","name":"edit_file","arguments":"{}","status":"in_progress"}}`,
			"",
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("1052-1st", upstream.URL),
	}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OpenAI/Python_2.31.0")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, `"type":"response.completed"`) {
		t.Fatalf("did not expect synthetic response.completed for added-only function_call, got %q", body)
	}
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "missing_response_completed") {
		t.Fatalf("expected strict missing completed error for added-only function_call, got %q", body)
	}
}

func TestResponsesStreamMissingCompletedWithOnlyFunctionCallArgumentsDeltaStaysStrict(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-tool-delta","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
			"",
			`data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"call-1","call_id":"call-1","type":"function_call","name":"edit_file","arguments":"","status":"in_progress"}}`,
			"",
			`data: {"type":"response.function_call_arguments.delta","sequence_number":3,"item_id":"call-1","output_index":0,"delta":"{\"path\""}`,
			"",
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("1052-1st", upstream.URL),
	}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OpenAI/Python_2.31.0")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, `"type":"response.completed"`) {
		t.Fatalf("did not expect synthetic response.completed for partial function_call arguments, got %q", body)
	}
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "missing_response_completed") {
		t.Fatalf("expected strict missing completed error for partial function_call arguments, got %q", body)
	}
}

func TestResponsesStreamExistingCompletedIsNotDuplicated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-complete","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
			"",
			`data: {"type":"response.output_text.delta","sequence_number":2,"output_index":0,"content_index":0,"item_id":"msg-complete","delta":"done"}`,
			"",
			`data: {"type":"response.completed","sequence_number":3,"response":{"id":"resp-complete","object":"response","status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5},"output":[{"type":"message","id":"msg-complete","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}]}}`,
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

	body := rec.Body.String()
	if got := strings.Count(body, "response.completed"); got != 1 {
		t.Fatalf("expected exactly one response.completed, got %d in %q", got, body)
	}
	if strings.Contains(body, "event: error") || strings.Contains(body, "message_delta") {
		t.Fatalf("did not expect error or Claude message_delta in completed Responses stream, got %q", body)
	}
}

type failOnNthWriteRecorder struct {
	httptest.ResponseRecorder
	failAt int
	writes int
}

func (r *failOnNthWriteRecorder) Write(data []byte) (int, error) {
	r.writes++
	if r.writes == r.failAt {
		return 0, fmt.Errorf("forced write failure")
	}
	return r.ResponseRecorder.Write(data)
}

func (r *failOnNthWriteRecorder) Flush() {}

func TestResponsesStreamSyntheticCompletionWriteFailureKeepsDownstreamReason(t *testing.T) {
	p := &Proxy{}
	endpoint := config.Endpoint{Name: "Primary", Transformer: "openai2", Model: "gpt-5.5"}
	upstream := strings.Join([]string{
		`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-write-fail","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
		"",
		`data: {"type":"response.output_text.delta","sequence_number":2,"output_index":0,"content_index":0,"item_id":"msg-write-fail","delta":"hello"}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstream)),
	}
	rec := &failOnNthWriteRecorder{failAt: 3}
	streamSession := newDownstreamStreamSession(rec, 0, ClientFormatOpenAIResponses)

	result := p.handleStreamingResponse(
		httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true}`)).Context(),
		rec,
		resp,
		endpoint,
		responses.NewOpenAI2Transformer("gpt-5.5"),
		"cx_resp_openai2",
		false,
		"gpt-5.5",
		[]byte(`{"stream":true}`),
		0,
		streamSession,
	)

	if result.Reason != streamFinishDownstreamWriteFailed {
		t.Fatalf("expected downstream write failure reason, got reason=%q err=%v", result.Reason, result.Err)
	}
}

func TestResponsesStreamMessageStopFallbackDoesNotInjectMessageDelta(t *testing.T) {
	p := &Proxy{}
	endpoint := config.Endpoint{Name: "Primary", Transformer: "openai2", Model: "gpt-5.5"}
	upstream := strings.Join([]string{
		`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-message-stop","object":"response","status":"in_progress","created_at":0,"model":"gpt-5.5","output":[]}}`,
		"",
		`data: {"type":"response.output_text.delta","sequence_number":2,"output_index":0,"content_index":0,"item_id":"msg-message-stop","delta":"hello"}`,
		"",
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
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
	streamSession := newDownstreamStreamSession(rec, 0, ClientFormatOpenAIResponses)

	result := p.handleStreamingResponse(
		httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true}`)).Context(),
		rec,
		resp,
		endpoint,
		responses.NewOpenAI2Transformer("gpt-5.5"),
		"cx_resp_openai2",
		false,
		"gpt-5.5",
		[]byte(`{"stream":true}`),
		0,
		streamSession,
	)

	body := rec.Body.String()
	if !result.Completed {
		t.Fatalf("expected text-only incomplete Responses stream to be completed synthetically, got reason=%q err=%v body=%q", result.Reason, result.Err, body)
	}
	if strings.Contains(body, "message_delta") {
		t.Fatalf("did not expect Claude message_delta fallback in OpenAI Responses stream, got %q", body)
	}
	if !strings.Contains(body, "response.completed") {
		t.Fatalf("expected synthetic response.completed, got %q", body)
	}
}
