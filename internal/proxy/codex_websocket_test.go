package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestCodexWebSocketURL(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		want    string
		wantErr bool
	}{
		{
			name:   "secure official endpoint",
			rawURL: "https://chatgpt.com/backend-api/codex/responses",
			want:   "wss://chatgpt.com/backend-api/codex/responses",
		},
		{
			name:   "local test endpoint",
			rawURL: "http://127.0.0.1:8080/backend-api/codex/responses",
			want:   "ws://127.0.0.1:8080/backend-api/codex/responses",
		},
		{
			name:    "unsupported scheme",
			rawURL:  "ftp://chatgpt.com/backend-api/codex/responses",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := codexWebSocketURL(tt.rawURL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got URL %q", tt.rawURL, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("codexWebSocketURL failed: %v", err)
			}
			if got != tt.want {
				t.Fatalf("codexWebSocketURL(%q) = %q, want %q", tt.rawURL, got, tt.want)
			}
		})
	}
}

func TestBuildCodexWebSocketFrameAddsResponseCreateType(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.5","input":[{"role":"user","content":"hi"}],"tools":[{"type":"custom","name":"exec"}],"prompt_cache_key":"trace-1"}`)

	frame, err := buildCodexWebSocketFrame(raw)
	if err != nil {
		t.Fatalf("buildCodexWebSocketFrame failed: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(frame, &payload); err != nil {
		t.Fatalf("unmarshal frame failed: %v", err)
	}
	if got := payload["type"]; got != "response.create" {
		t.Fatalf("type = %#v, want response.create", got)
	}
	if got := payload["model"]; got != "gpt-5.5" {
		t.Fatalf("model = %#v, want gpt-5.5", got)
	}
	if got := payload["prompt_cache_key"]; got != "trace-1" {
		t.Fatalf("prompt_cache_key = %#v, want trace-1", got)
	}
	if tools, ok := payload["tools"].([]interface{}); !ok || len(tools) != 1 {
		t.Fatalf("expected tools to be preserved, got %#v", payload["tools"])
	}
	if input, ok := payload["input"].([]interface{}); !ok || len(input) != 1 {
		t.Fatalf("expected input to be preserved, got %#v", payload["input"])
	}
}

func TestBuildCodexWebSocketFrameRejectsInvalidPayload(t *testing.T) {
	if _, err := buildCodexWebSocketFrame([]byte(`not-json`)); err == nil {
		t.Fatal("expected invalid payload error")
	}
}

func TestOpenCodexWebSocketStreamBridgesCompletedResponseToSSE(t *testing.T) {
	requestErr := make(chan error, 1)
	largeDelta := strings.Repeat("x", 3*1024*1024)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			requestErr <- &testUnexpectedValueError{field: "Authorization", got: got, want: "Bearer test-token"}
			return
		}
		if got := r.Header.Get("Chatgpt-Account-Id"); got != "acct-1" {
			requestErr <- &testUnexpectedValueError{field: "Chatgpt-Account-Id", got: got, want: "acct-1"}
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			requestErr <- err
			return
		}
		defer conn.Close()

		_, frame, err := conn.ReadMessage()
		if err != nil {
			requestErr <- err
			return
		}
		var request map[string]interface{}
		if err := json.Unmarshal(frame, &request); err != nil {
			requestErr <- err
			return
		}
		if request["type"] != "response.create" {
			requestErr <- &testUnexpectedValueError{field: "type", got: request["type"], want: "response.create"}
			return
		}

		for _, event := range []map[string]interface{}{
			{"type": "response.created", "response": map[string]interface{}{"id": "resp-ws", "status": "in_progress"}},
			{"type": "response.custom_tool_call_input.delta", "delta": largeDelta},
			{"type": "response.completed", "response": map[string]interface{}{"id": "resp-ws", "status": "completed"}},
		} {
			if err := conn.WriteJSON(event); err != nil {
				requestErr <- err
				return
			}
		}
		requestErr <- nil
	}))
	defer upstream.Close()

	proxyReq, err := http.NewRequest(http.MethodPost, upstream.URL+"/backend-api/codex/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	proxyReq.Header.Set("Authorization", "Bearer test-token")
	proxyReq.Header.Set("Chatgpt-Account-Id", "acct-1")
	payload := []byte(`{"model":"gpt-5.5","stream":true,"input":[]}`)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := (&Proxy{}).openCodexWebSocketStream(ctx, proxyReq, config.Endpoint{Name: "Codex Pool"}, payload)
	if err != nil {
		t.Fatalf("openCodexWebSocketStream failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read bridged SSE failed: %v", err)
	}
	if err := <-requestErr; err != nil {
		t.Fatalf("upstream validation failed: %v", err)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	bodyText := string(body)
	for _, want := range []string{"response.created", "response.custom_tool_call_input.delta", "response.completed", largeDelta} {
		if !strings.Contains(bodyText, want) {
			t.Fatalf("bridged SSE missing %q", want[:min(len(want), 80)])
		}
	}
}

func TestOpenCodexWebSocketStreamReturnsReadErrorWhenClosedBeforeCompleted(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"type":         "response.custom_tool_call_input.delta",
			"delta":        "partial",
			"item_id":      "tool-1",
			"call_id":      "call-1",
			"output_index": 0,
		})
	}))
	defer upstream.Close()

	proxyReq, err := http.NewRequest(http.MethodPost, upstream.URL+"/backend-api/codex/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := (&Proxy{}).openCodexWebSocketStream(ctx, proxyReq, config.Endpoint{Name: "Codex Pool"}, []byte(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	if err != nil {
		t.Fatalf("openCodexWebSocketStream failed: %v", err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr == nil {
		t.Fatalf("expected premature close error, got body %q", string(body))
	}
	if !strings.Contains(readErr.Error(), "before response.completed") {
		t.Fatalf("expected missing completion error, got %v", readErr)
	}
}

type testUnexpectedValueError struct {
	field string
	got   interface{}
	want  interface{}
}

func (e *testUnexpectedValueError) Error() string {
	return e.field + " mismatch: got " + stringifyTestValue(e.got) + ", want " + stringifyTestValue(e.want)
}

func stringifyTestValue(value interface{}) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestCodexTokenPoolStreamingResponsesPreferWebSocket(t *testing.T) {
	var httpHits int
	p := newCodexWebSocketRoutingTestProxy(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		httpHits++
		return nil, errors.New("HTTP upstream should not be called")
	}))

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for _, event := range []map[string]interface{}{
			{"type": "response.created", "response": map[string]interface{}{"id": "resp-ws-route", "status": "in_progress"}},
			{"type": "response.output_text.delta", "delta": "ok", "item_id": "msg-1", "output_index": 0, "content_index": 0},
			{"type": "response.completed", "response": map[string]interface{}{
				"id": "resp-ws-route", "status": "completed",
				"usage": map[string]interface{}{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
				"output": []interface{}{map[string]interface{}{
					"type": "message", "id": "msg-1", "role": "assistant", "status": "completed",
					"content": []interface{}{map[string]interface{}{"type": "output_text", "text": "ok"}},
				}},
			}},
		} {
			if err := conn.WriteJSON(event); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	localWSURL, err := codexWebSocketURL(upstream.URL + "/backend-api/codex/responses")
	if err != nil {
		t.Fatal(err)
	}
	var dialHits int
	p.codexWebSocketDial = func(ctx context.Context, target string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialHits++
		if target != "wss://chatgpt.com/backend-api/codex/responses" {
			t.Fatalf("unexpected official websocket target %q", target)
		}
		return websocket.DefaultDialer.DialContext(ctx, localWSURL, headers)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Codex_Desktop/0.142.0-alpha.1")
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)

	if dialHits != 1 {
		t.Fatalf("expected one websocket dial, got %d", dialHits)
	}
	if httpHits != 0 {
		t.Fatalf("expected no HTTP upstream calls, got %d", httpHits)
	}
	if body := rec.Body.String(); !strings.Contains(body, "response.completed") || !strings.Contains(body, `"delta":"ok"`) {
		t.Fatalf("expected completed websocket-backed SSE, got %q", body)
	}
}

func TestCodexWebSocketUnsupportedHandshakeFallsBackToHTTP(t *testing.T) {
	var httpHits int
	p := newCodexWebSocketRoutingTestProxy(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		httpHits++
		return completedCodexHTTPTestResponse(req), nil
	}))
	var dialHits int
	p.codexWebSocketDial = func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error) {
		dialHits++
		return nil, &http.Response{
			StatusCode: http.StatusUpgradeRequired,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("upgrade required")),
		}, websocket.ErrBadHandshake
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Codex_Desktop/0.142.0-alpha.1")
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)

	if dialHits != 1 || httpHits != 1 {
		t.Fatalf("expected one websocket dial and one HTTP fallback, got dial=%d http=%d", dialHits, httpHits)
	}
	if body := rec.Body.String(); !strings.Contains(body, "response.completed") {
		t.Fatalf("expected completed HTTP fallback stream, got %q", body)
	}
}

func TestNonCodexResponsesDoNotUseCodexWebSocket(t *testing.T) {
	var httpHits int
	endpoint := failoverPolicyTestEndpoint("OpenAI", "https://api.example.com")
	p := newFailoverPolicyTestProxy([]config.Endpoint{endpoint}, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		httpHits++
		return completedCodexHTTPTestResponse(req), nil
	})})
	var dialHits int
	p.codexWebSocketDial = func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error) {
		dialHits++
		return nil, nil, errors.New("unexpected websocket dial")
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)

	if dialHits != 0 {
		t.Fatalf("expected no websocket dial, got %d", dialHits)
	}
	if httpHits != 1 {
		t.Fatalf("expected one HTTP request, got %d", httpHits)
	}
}

func TestCodexPendingCustomToolMissingCompletionIsNeverTolerated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp-tool","status":"in_progress","output":[]}}`,
			"",
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","id":"tool-1","call_id":"call-1","name":"exec","status":"in_progress","input":""}}`,
			"",
			`data: {"type":"response.custom_tool_call_input.delta","output_index":0,"item_id":"tool-1","delta":"partial"}`,
			"",
			"",
		}, "\n")))
	}))
	defer upstream.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{failoverPolicyTestEndpoint("Primary", upstream.URL)}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Codex_Desktop/0.142.0-alpha.1")
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, streamFinishMissingResponsesDone) {
		t.Fatalf("expected typed missing-completion error for pending tool, got %q", body)
	}
}

func newCodexWebSocketRoutingTestProxy(t *testing.T, transport http.RoundTripper) *Proxy {
	t.Helper()
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	credential := storage.EndpointCredential{
		EndpointName: "Codex Pool",
		ProviderType: storage.ProviderTypeCodex,
		AccessToken:  "test-token",
		AccountID:    "acct-1",
		Status:       "active",
		Enabled:      true,
	}
	if err := store.SaveEndpointCredential(&credential); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{
		Name:        "Codex Pool",
		APIUrl:      config.CodexTokenPoolAPIURL,
		AuthMode:    config.AuthModeCodexTokenPool,
		Enabled:     true,
		Transformer: config.CodexTokenPoolTransformer,
		Model:       "gpt-5.5",
	}})
	p := New(cfg, &noopStatsStorage{}, store, "test-device")
	p.httpClient = &http.Client{Transport: transport}
	p.retrySleep = func(time.Duration) {}
	return p
}

func completedCodexHTTPTestResponse(req *http.Request) *http.Response {
	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp-http","status":"in_progress","output":[]}}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"ok","item_id":"msg-1","output_index":0,"content_index":0}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp-http","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2},"output":[{"type":"message","id":"msg-1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}]}}`,
		"",
		"",
	}, "\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}
