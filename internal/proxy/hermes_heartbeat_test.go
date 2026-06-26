package proxy

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
)

// TestOpenAIResponsesStreamHeartbeatIsResponseCreated verifies that AINexus
// sends a response.created event as the initial keep-alive for OpenAI Responses
// API streaming clients (Hermes / Python SDK openai>=1.0).
//
// The Python SDK's ResponseStreamState._create_initial_response() hard-requires
// that the very first parsed event has type=="response.created"; any other type
// raises RuntimeError which cancels the connection. SSE comments (": ...") and
// other event types (response.in_progress, etc.) all trigger this error.
func TestOpenAIResponsesStreamHeartbeatIsResponseCreated(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		// Slow upstream: silent for longer than one heartbeat interval.
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"hello"}`,
			"",
			`data: {"type":"response.completed","response":{"id":"r1","object":"response","status":"completed","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n")))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	endpoint := failoverPolicyTestEndpoint("Primary", upstream.URL)
	p := newFailoverPolicyTestProxy([]config.Endpoint{endpoint}, upstream.Client())
	p.streamHeartbeatInterval = 30 * time.Millisecond

	proxySrv := httptest.NewServer(http.HandlerFunc(p.handleProxy))
	defer proxySrv.Close()

	firstDataCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		resp, err := proxySrv.Client().Post(
			proxySrv.URL+"/v1/responses",
			"application/json",
			strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`),
		)
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				firstDataCh <- line
				return
			}
		}
		firstDataCh <- ""
	}()

	select {
	case err := <-errCh:
		t.Fatalf("request failed: %v", err)
	case line := <-firstDataCh:
		if line == "" {
			t.Fatal("no data: event received; Python SDK would raise RuntimeError and cancel")
		}
		jsonPart := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if !strings.Contains(jsonPart, `"type":"response.created"`) {
			t.Fatalf("Python SDK requires first event type==response.created, got: %q", line)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("no data: event within 100ms; heartbeat was only SSE comments which Python SDK skips")
	}
}

func TestOpenAIResponsesStreamHeartbeatDoesNotReplaceRealResponseID(t *testing.T) {
	rec := httptest.NewRecorder()
	session := newDownstreamStreamSession(rec, time.Hour, ClientFormatOpenAIResponses)

	if _, err := session.writeOpenAIResponsesWaitingCreatedIfNeeded(); err != nil {
		t.Fatalf("writeOpenAIResponsesWaitingCreatedIfNeeded failed: %v", err)
	}

	realCreated := []byte(`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-real","object":"response","status":"in_progress","output":[]}}` + "\n\n")
	if err := session.Write(realCreated); err != nil {
		t.Fatalf("Write real response.created failed: %v", err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"id":"resp-real"`) {
		t.Fatalf("expected real response id to reach client, got body: %s", body)
	}
}

// TestOpenAIResponsesStreamFromChatUpstreamEmitsCreatedOnce verifies that when
// a Codex Desktop /v1/responses streaming client hits a Poe (OpenAI Chat)
// upstream behind AINexus, the downstream SSE body contains exactly one
// response.created event. Real Poe usage shows Codex cancelling the connection
// ~10–14 s after start, which aligns with a bogus heartbeat response.created
// landing on the open stream while the upstream has already billed for tokens.
//
// The path under test: ClientFormatOpenAIResponses → endpoint trans=openai
// (cx_resp_openai) → /v1/chat/completions → OpenAIStreamToOpenAI2.
func TestOpenAIResponsesStreamFromChatUpstreamEmitsCreatedOnce(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	const firstTokenDelay = 50 * time.Millisecond

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		time.Sleep(firstTokenDelay)
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"claude-opus-4.8","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`,
			"",
			`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"claude-opus-4.8","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
			"",
			`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"claude-opus-4.8","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n")))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	endpoint := failoverPolicyTestEndpoint("primary", upstream.URL)
	endpoint.Transformer = "openai"
	endpoint.Model = "claude-opus-4.8"
	p := newFailoverPolicyTestProxy([]config.Endpoint{endpoint}, upstream.Client())
	p.streamHeartbeatInterval = 20 * time.Millisecond

	proxySrv := httptest.NewServer(http.HandlerFunc(p.handleProxy))
	defer proxySrv.Close()

	resp, err := proxySrv.Client().Post(
		proxySrv.URL+"/v1/responses",
		"application/json",
		strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi"}`),
	)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var fullBody strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		fullBody.WriteString(scanner.Text())
		fullBody.WriteByte('\n')
	}
	body := fullBody.String()

	if !strings.Contains(body, "hello") || !strings.Contains(body, "world") {
		t.Fatalf("expected downstream stream to contain transformed text, got: %s", body)
	}

	createdCount := 0
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		jsonPart := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonPart == "" || jsonPart == "[DONE]" {
			continue
		}
		if strings.Contains(jsonPart, `"type":"response.created"`) {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf(
			"expected exactly 1 response.created event in downstream SSE (got %d). Duplicate created events corrupt Codex stream protocol and cause client-side cancellation.",
			createdCount,
		)
	}
}

func TestPoeResponsesClientUsesNativeResponsesStreaming(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var upstreamPath string
	var upstreamBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		upstreamBody = string(body)

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","sequence_number":1,"response":{"id":"resp-poe-1","object":"response","status":"in_progress","model":"claude-opus-4.8","output":[]}}`,
			"",
			`data: {"type":"response.output_text.delta","sequence_number":2,"output_index":0,"content_index":0,"item_id":"msg-poe-1","delta":"hello"}`,
			"",
			`data: {"type":"response.output_text.delta","sequence_number":3,"output_index":0,"content_index":0,"item_id":"msg-poe-1","delta":" world"}`,
			"",
			`data: {"type":"response.completed","sequence_number":4,"response":{"id":"resp-poe-1","object":"response","status":"completed","model":"claude-opus-4.8","usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10},"output":[{"type":"message","id":"msg-poe-1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello world"}]}]}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n")))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	endpoint := failoverPolicyTestEndpoint("Poe", upstream.URL)
	endpoint.Transformer = "poe"
	endpoint.Model = "claude-opus-4.8"
	p := newFailoverPolicyTestProxy([]config.Endpoint{endpoint}, upstream.Client())
	p.streamHeartbeatInterval = time.Hour

	proxySrv := httptest.NewServer(http.HandlerFunc(p.handleProxy))
	defer proxySrv.Close()

	resp, err := proxySrv.Client().Post(
		proxySrv.URL+"/v1/responses",
		"application/json",
		strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":"hi","reasoning":{"effort":"high"}}`),
	)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var fullBody strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		fullBody.WriteString(scanner.Text())
		fullBody.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("failed reading downstream stream: %v", err)
	}
	body := fullBody.String()

	if upstreamPath != "/v1/responses" {
		t.Fatalf("expected Poe upstream path /v1/responses, got %s", upstreamPath)
	}
	if strings.Contains(upstreamBody, "output_effort") {
		t.Fatalf("did not expect Chat-only output_effort in native Poe Responses request: %s", upstreamBody)
	}
	if !strings.Contains(upstreamBody, `"reasoning":{"effort":"high"}`) {
		t.Fatalf("expected native Responses reasoning effort to be preserved, got: %s", upstreamBody)
	}
	if strings.Count(body, `"type":"response.created"`) != 1 {
		t.Fatalf("expected exactly one response.created event, got stream: %s", body)
	}
	for _, want := range []string{
		`"type":"response.output_text.delta"`,
		`"delta":"hello"`,
		`"delta":" world"`,
		`"type":"response.completed"`,
		`data: [DONE]`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected downstream stream to contain %s, got: %s", want, body)
		}
	}

	var logs strings.Builder
	for _, entry := range logger.GetLogger().GetLogs() {
		logs.WriteString(entry.Message)
		logs.WriteByte('\n')
	}
	joinedLogs := logs.String()
	for _, want := range []string{
		"Poe streaming diagnostics",
		"upstream_path=/v1/responses",
		"content_type=text/event-stream",
		"first_transformed_event_type=response.created",
	} {
		if !strings.Contains(joinedLogs, want) {
			t.Fatalf("expected logs to contain %q; logs:\n%s", want, joinedLogs)
		}
	}
}
