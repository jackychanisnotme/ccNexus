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
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
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

func TestOpenCodexWebSocketStreamPreservesSafeUpstreamErrorDetails(t *testing.T) {
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
			"type":   "error",
			"status": http.StatusTooManyRequests,
			"error": map[string]interface{}{
				"type":    "usage_limit_reached",
				"code":    "usage_limit_reached",
				"message": "The usage limit has been reached",
			},
			"headers": map[string]interface{}{
				"x-codex-primary-used-percent": 100,
				"x-codex-primary-reset-at":     1782217538,
				"authorization":                "Bearer must-not-leak",
			},
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
	_, readErr := io.ReadAll(resp.Body)
	if readErr == nil {
		t.Fatal("expected websocket upstream error")
	}

	var upstreamErr *codexWebSocketUpstreamError
	if !errors.As(readErr, &upstreamErr) {
		t.Fatalf("expected codexWebSocketUpstreamError, got %T: %v", readErr, readErr)
	}
	if upstreamErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", upstreamErr.StatusCode)
	}
	if upstreamErr.Type != "usage_limit_reached" || upstreamErr.Code != "usage_limit_reached" {
		t.Fatalf("unexpected upstream error identity: type=%q code=%q", upstreamErr.Type, upstreamErr.Code)
	}
	if upstreamErr.Message != "The usage limit has been reached" {
		t.Fatalf("message = %q", upstreamErr.Message)
	}
	if got := upstreamErr.Headers.Get("X-Codex-Primary-Used-Percent"); got != "100" {
		t.Fatalf("used percent header = %q, want 100", got)
	}
	if got := upstreamErr.Headers.Get("X-Codex-Primary-Reset-At"); got != "1782217538" {
		t.Fatalf("reset header = %q, want 1782217538", got)
	}
	if got := upstreamErr.Headers.Get("Authorization"); got != "" {
		t.Fatalf("unsafe header was preserved: %q", got)
	}
	if strings.Contains(readErr.Error(), "must-not-leak") {
		t.Fatalf("error string leaked unsafe header: %v", readErr)
	}
}

func TestRetryReasonForCodexWebSocketUpstreamError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   string
	}{
		{name: "invalid request", status: http.StatusBadRequest, want: "invalid_request"},
		{name: "unauthorized", status: http.StatusUnauthorized, want: retryReasonEndpointAuthFailed},
		{name: "forbidden", status: http.StatusForbidden, want: retryReasonEndpointAuthFailed},
		{name: "rate limited", status: http.StatusTooManyRequests, want: "rate_limited"},
		{name: "upstream failure", status: http.StatusServiceUnavailable, want: "upstream_5xx"},
		{name: "missing status", status: 0, want: streamFinishUpstreamStreamError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &codexWebSocketUpstreamError{StatusCode: tt.status}
			if got := retryReasonForCodexWebSocketUpstreamError(err); got != tt.want {
				t.Fatalf("reason = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCodexWebSocketRateLimitRotatesCredentialWithoutCoolingEndpoint(t *testing.T) {
	logger.GetLogger().Clear()
	t.Cleanup(func() { logger.GetLogger().Clear() })
	store, firstCredential, secondCredential := newCodexWebSocketCredentialPool(t)
	p := newCodexWebSocketProxyWithStore(t, store, []config.Endpoint{{
		Name:        "Codex Pool",
		APIUrl:      config.CodexTokenPoolAPIURL,
		AuthMode:    config.AuthModeCodexTokenPool,
		Enabled:     true,
		Transformer: config.CodexTokenPoolTransformer,
		Model:       "gpt-5.5",
	}})

	resetAt := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
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
		switch r.Header.Get("Authorization") {
		case "Bearer first-token":
			_ = conn.WriteJSON(map[string]interface{}{
				"type":   "error",
				"status": http.StatusTooManyRequests,
				"error": map[string]interface{}{
					"type":    "usage_limit_reached",
					"code":    "usage_limit_reached",
					"message": "The usage limit has been reached",
				},
				"headers": map[string]interface{}{
					"x-codex-primary-used-percent": 100,
					"x-codex-primary-reset-at":     resetAt.Unix(),
				},
			})
		case "Bearer second-token":
			writeCompletedCodexWebSocketResponse(conn, "rotated")
		}
	}))
	defer upstream.Close()
	localWSURL, err := codexWebSocketURL(upstream.URL + "/backend-api/codex/responses")
	if err != nil {
		t.Fatal(err)
	}
	var usedAuthorizations []string
	p.codexWebSocketDial = func(ctx context.Context, _ string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		usedAuthorizations = append(usedAuthorizations, headers.Get("Authorization"))
		return websocket.DefaultDialer.DialContext(ctx, localWSURL, headers)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OpenAI/Python_2.31.0")
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)

	if got := strings.Join(usedAuthorizations, ","); got != "Bearer first-token,Bearer second-token" {
		t.Fatalf("credential sequence = %q", got)
	}
	if body := rec.Body.String(); !strings.Contains(body, "response.completed") || !strings.Contains(body, "rotated") {
		t.Fatalf("expected successful rotated response, got %q", body)
	}
	limits, err := store.GetCredentialRateLimits(firstCredential.ID)
	if err != nil {
		t.Fatalf("get rate limits: %v", err)
	}
	if limits == nil || limits.Data == nil || limits.Data.Snapshot == nil || limits.Data.Snapshot.Primary == nil {
		t.Fatalf("missing persisted rate limits: %#v", limits)
	}
	if limits.Data.Snapshot.Primary.UsedPercent != 100 {
		t.Fatalf("used percent = %v, want 100", limits.Data.Snapshot.Primary.UsedPercent)
	}
	firstAfter, err := store.GetCredentialByID(firstCredential.ID)
	if err != nil {
		t.Fatalf("get first credential: %v", err)
	}
	if firstAfter == nil || firstAfter.Status != "cooldown" || firstAfter.CooldownUntil == nil {
		t.Fatalf("expected first credential cooldown, got %#v", firstAfter)
	}
	if firstAfter.CooldownUntil.Before(resetAt.Add(-time.Second)) {
		t.Fatalf("cooldown until = %s, want at least %s", firstAfter.CooldownUntil, resetAt)
	}
	secondAfter, err := store.GetCredentialByID(secondCredential.ID)
	if err != nil {
		t.Fatalf("get second credential: %v", err)
	}
	if secondAfter == nil || secondAfter.Status != "active" {
		t.Fatalf("expected second credential active, got %#v", secondAfter)
	}
	if p.isEndpointInActiveCooldown("Codex Pool") {
		t.Fatal("credential-scoped rate limit must not cool the endpoint")
	}
	for _, entry := range logger.GetLogger().GetLogs() {
		for _, secret := range []string{"first-token", "second-token", "acct-1", "acct-2"} {
			if strings.Contains(entry.Message, secret) {
				t.Fatalf("log entry leaked credential data %q: %s", secret, entry.Message)
			}
		}
	}
}

func TestCodexTokenPoolKnownExhaustionFailsOverWithoutDialOrEndpointCooldown(t *testing.T) {
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	credential := &storage.EndpointCredential{
		EndpointName: "Codex Pool",
		ProviderType: storage.ProviderTypeCodex,
		AccessToken:  "exhausted-token",
		Status:       "active",
		Enabled:      true,
	}
	if err := store.SaveEndpointCredential(credential); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	resetAt := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	resetUnix := resetAt.Unix()
	snapshot := storage.CodexRateLimitSnapshot{
		LimitID: "codex",
		Primary: &storage.CodexRateLimitWindow{UsedPercent: 100, ResetsAt: &resetUnix},
	}
	if err := store.UpsertCredentialRateLimits(credential.ID, &storage.CodexRateLimitsData{
		Snapshot:  &snapshot,
		ByLimitID: map[string]storage.CodexRateLimitSnapshot{"codex": snapshot},
		Source:    "test",
	}, "ok", "", time.Now().UTC()); err != nil {
		t.Fatalf("save rate limits: %v", err)
	}

	var backupHits int
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupHits++
		response := completedCodexHTTPTestResponse(r)
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	defer backup.Close()

	p := newCodexWebSocketProxyWithStore(t, store, []config.Endpoint{
		{
			Name: "Codex Pool", APIUrl: config.CodexTokenPoolAPIURL,
			AuthMode: config.AuthModeCodexTokenPool, Enabled: true,
			Transformer: config.CodexTokenPoolTransformer, Model: "gpt-5.5",
		},
		{
			Name: "Backup", APIUrl: backup.URL,
			AuthMode: config.AuthModeAPIKey, APIKey: "backup-key", Enabled: true,
			Transformer: "openai2", Model: "gpt-5.5",
		},
	})
	p.httpClient = backup.Client()
	var dialHits int
	p.codexWebSocketDial = func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error) {
		dialHits++
		return nil, nil, errors.New("exhausted credential must not be dialed")
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OpenAI/Python_2.31.0")
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)

	if dialHits != 0 {
		t.Fatalf("expected no websocket dial, got %d", dialHits)
	}
	if backupHits != 1 {
		t.Fatalf("expected one backup request, got %d", backupHits)
	}
	if body := rec.Body.String(); !strings.Contains(body, "response.completed") {
		t.Fatalf("expected backup completion, got %q", body)
	}
	if p.isEndpointInActiveCooldown("Codex Pool") {
		t.Fatal("known credential exhaustion must not cool the endpoint")
	}
}

func TestCodexWebSocketBadRequestReturnsTypedErrorWithoutRetry(t *testing.T) {
	store, firstCredential, secondCredential := newCodexWebSocketCredentialPool(t)
	p := newCodexWebSocketProxyWithStore(t, store, []config.Endpoint{{
		Name: "Codex Pool", APIUrl: config.CodexTokenPoolAPIURL,
		AuthMode: config.AuthModeCodexTokenPool, Enabled: true,
		Transformer: config.CodexTokenPoolTransformer, Model: "gpt-5.5",
	}})
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
			"type":   "error",
			"status": http.StatusBadRequest,
			"error": map[string]interface{}{
				"type": "invalid_request_error", "code": "unsupported_parameter", "message": "temperature is not supported",
			},
		})
	}))
	defer upstream.Close()
	localWSURL, err := codexWebSocketURL(upstream.URL + "/backend-api/codex/responses")
	if err != nil {
		t.Fatal(err)
	}
	var dialHits int
	p.codexWebSocketDial = func(ctx context.Context, _ string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		dialHits++
		return websocket.DefaultDialer.DialContext(ctx, localWSURL, headers)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)

	if dialHits != 1 {
		t.Fatalf("expected one websocket request, got %d", dialHits)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "invalid_request") || !strings.Contains(body, "temperature is not supported") {
		t.Fatalf("expected typed invalid request error, got %q", body)
	}
	for _, credentialID := range []int64{firstCredential.ID, secondCredential.ID} {
		credential, err := store.GetCredentialByID(credentialID)
		if err != nil {
			t.Fatalf("get credential: %v", err)
		}
		if credential == nil || credential.Status != "active" || credential.FailureCount != 0 {
			t.Fatalf("bad request must not penalize credential: %#v", credential)
		}
	}
	if p.isEndpointInActiveCooldown("Codex Pool") {
		t.Fatal("bad request must not cool endpoint")
	}
}

func TestCodexWebSocketUnauthorizedRotatesCredential(t *testing.T) {
	store, firstCredential, secondCredential := newCodexWebSocketCredentialPool(t)
	p := newCodexWebSocketProxyWithStore(t, store, []config.Endpoint{{
		Name: "Codex Pool", APIUrl: config.CodexTokenPoolAPIURL,
		AuthMode: config.AuthModeCodexTokenPool, Enabled: true,
		Transformer: config.CodexTokenPoolTransformer, Model: "gpt-5.5",
	}})
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
		if r.Header.Get("Authorization") == "Bearer first-token" {
			_ = conn.WriteJSON(map[string]interface{}{
				"type": "error", "status": http.StatusUnauthorized,
				"error": map[string]interface{}{"type": "authentication_error", "code": "invalid_token", "message": "token expired"},
			})
			return
		}
		writeCompletedCodexWebSocketResponse(conn, "authorized")
	}))
	defer upstream.Close()
	localWSURL, err := codexWebSocketURL(upstream.URL + "/backend-api/codex/responses")
	if err != nil {
		t.Fatal(err)
	}
	var usedAuthorizations []string
	p.codexWebSocketDial = func(ctx context.Context, _ string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		usedAuthorizations = append(usedAuthorizations, headers.Get("Authorization"))
		return websocket.DefaultDialer.DialContext(ctx, localWSURL, headers)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)

	if got := strings.Join(usedAuthorizations, ","); got != "Bearer first-token,Bearer second-token" {
		t.Fatalf("credential sequence = %q", got)
	}
	if body := rec.Body.String(); !strings.Contains(body, "response.completed") || !strings.Contains(body, "authorized") {
		t.Fatalf("expected successful rotated response, got %q", body)
	}
	firstAfter, err := store.GetCredentialByID(firstCredential.ID)
	if err != nil {
		t.Fatalf("get first credential: %v", err)
	}
	if firstAfter == nil || firstAfter.Status != "invalid" {
		t.Fatalf("expected unauthorized credential invalid, got %#v", firstAfter)
	}
	secondAfter, err := store.GetCredentialByID(secondCredential.ID)
	if err != nil {
		t.Fatalf("get second credential: %v", err)
	}
	if secondAfter == nil || secondAfter.Status != "active" {
		t.Fatalf("expected second credential active, got %#v", secondAfter)
	}
	if p.isEndpointInActiveCooldown("Codex Pool") {
		t.Fatal("credential auth failure must not cool endpoint")
	}
}

func TestCodexWebSocketServerErrorRetainsStreamRetryBehavior(t *testing.T) {
	p := newCodexWebSocketRoutingTestProxy(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected HTTP upstream request")
	}))
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var requestCount atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if requestCount.Add(1) == 1 {
			_ = conn.WriteJSON(map[string]interface{}{
				"type": "error", "status": http.StatusServiceUnavailable,
				"error": map[string]interface{}{"type": "server_error", "code": "overloaded", "message": "temporarily overloaded"},
			})
			return
		}
		writeCompletedCodexWebSocketResponse(conn, "recovered")
	}))
	defer upstream.Close()
	localWSURL, err := codexWebSocketURL(upstream.URL + "/backend-api/codex/responses")
	if err != nil {
		t.Fatal(err)
	}
	p.codexWebSocketDial = func(ctx context.Context, _ string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		return websocket.DefaultDialer.DialContext(ctx, localWSURL, headers)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)

	if got := requestCount.Load(); got != 2 {
		t.Fatalf("expected one retry after 503, got %d requests", got)
	}
	if body := rec.Body.String(); !strings.Contains(body, "response.completed") || !strings.Contains(body, "recovered") {
		t.Fatalf("expected successful retry response, got %q", body)
	}
	if p.isEndpointInActiveCooldown("Codex Pool") {
		t.Fatal("successful retry must clear endpoint cooldown")
	}
}

func newCodexWebSocketCredentialPool(t *testing.T) (*storage.SQLiteStorage, *storage.EndpointCredential, *storage.EndpointCredential) {
	t.Helper()
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	firstUsed := time.Now().UTC().Add(-2 * time.Hour)
	secondUsed := time.Now().UTC().Add(-time.Hour)
	first := &storage.EndpointCredential{EndpointName: "Codex Pool", ProviderType: storage.ProviderTypeCodex, AccessToken: "first-token", AccountID: "acct-1", Status: "active", Enabled: true, LastUsedAt: &firstUsed}
	second := &storage.EndpointCredential{EndpointName: "Codex Pool", ProviderType: storage.ProviderTypeCodex, AccessToken: "second-token", AccountID: "acct-2", Status: "active", Enabled: true, LastUsedAt: &secondUsed}
	for _, credential := range []*storage.EndpointCredential{first, second} {
		if err := store.SaveEndpointCredential(credential); err != nil {
			t.Fatalf("save credential: %v", err)
		}
	}
	return store, first, second
}

func newCodexWebSocketProxyWithStore(t *testing.T, store *storage.SQLiteStorage, endpoints []config.Endpoint) *Proxy {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints(endpoints)
	p := New(cfg, &noopStatsStorage{}, store, "test-device")
	p.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected HTTP upstream request")
	})}
	p.retrySleep = func(time.Duration) {}
	return p
}

func writeCompletedCodexWebSocketResponse(conn *websocket.Conn, text string) {
	events := []map[string]interface{}{
		{"type": "response.created", "response": map[string]interface{}{"id": "resp-ws-rotated", "status": "in_progress", "output": []interface{}{}}},
		{"type": "response.output_text.delta", "delta": text, "item_id": "msg-1", "output_index": 0, "content_index": 0},
		{"type": "response.completed", "response": map[string]interface{}{
			"id": "resp-ws-rotated", "status": "completed",
			"usage": map[string]interface{}{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
			"output": []interface{}{map[string]interface{}{
				"type": "message", "id": "msg-1", "role": "assistant", "status": "completed",
				"content": []interface{}{map[string]interface{}{"type": "output_text", "text": text}},
			}},
		}},
	}
	for _, event := range events {
		if err := conn.WriteJSON(event); err != nil {
			return
		}
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

func TestCodexTokenPoolWebSocketRequestIncludesFastServiceTier(t *testing.T) {
	var httpHits int
	p := newCodexWebSocketRoutingTestProxy(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		httpHits++
		return nil, errors.New("HTTP upstream should not be called")
	}))
	endpoints := p.config.GetEndpoints()
	endpoints[0].CodexFastMode = true
	p.config.UpdateEndpoints(endpoints)

	frameCh := make(chan map[string]interface{}, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, frame, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var request map[string]interface{}
		if err := json.Unmarshal(frame, &request); err != nil {
			return
		}
		frameCh <- request
		writeCompletedCodexWebSocketResponse(conn, "fast")
	}))
	defer upstream.Close()
	localWSURL, err := codexWebSocketURL(upstream.URL + "/backend-api/codex/responses")
	if err != nil {
		t.Fatal(err)
	}
	p.codexWebSocketDial = func(ctx context.Context, _ string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		return websocket.DefaultDialer.DialContext(ctx, localWSURL, headers)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Codex_Desktop/0.142.0-alpha.1")
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)

	if httpHits != 0 {
		t.Fatalf("expected no HTTP upstream calls, got %d", httpHits)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rec.Code, rec.Body.String())
	}
	select {
	case frame := <-frameCh:
		if frame["service_tier"] != "fast" {
			t.Fatalf("expected websocket frame service_tier=fast, got %#v in %#v", frame["service_tier"], frame)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket request frame")
	}
}

func TestCodexWebSocketRateLimitsAreCapturedButNotForwarded(t *testing.T) {
	logger.GetLogger().Clear()
	t.Cleanup(func() { logger.GetLogger().Clear() })
	p := newCodexWebSocketRoutingTestProxy(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected HTTP upstream request")
	}))
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	resetAt := time.Now().UTC().Add(5 * time.Hour).Unix()
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
			"type":      "codex.rate_limits",
			"plan_type": "plus",
			"rate_limits": map[string]interface{}{
				"primary": map[string]interface{}{
					"used_percent": 5, "window_minutes": 300, "reset_at": resetAt,
				},
				"secondary": map[string]interface{}{
					"used_percent": 29, "window_minutes": 10080,
				},
			},
			"credits": map[string]interface{}{"has_credits": false, "unlimited": false},
		})
		writeCompletedCodexWebSocketResponse(conn, "healthy")
	}))
	defer upstream.Close()
	localWSURL, err := codexWebSocketURL(upstream.URL + "/backend-api/codex/responses")
	if err != nil {
		t.Fatal(err)
	}
	p.codexWebSocketDial = func(ctx context.Context, _ string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		return websocket.DefaultDialer.DialContext(ctx, localWSURL, headers)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OpenAI/Python_2.31.0")
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "codex.rate_limits") {
		t.Fatalf("internal rate limit event leaked downstream: %q", body)
	}
	if got := sseBlockEventType(body); got != "response.created" {
		t.Fatalf("first downstream event type = %q, want response.created; body=%q", got, body)
	}
	if !strings.Contains(body, "response.completed") || !strings.Contains(body, "healthy") {
		t.Fatalf("expected completed response, got %q", body)
	}
	credentials, err := p.storage.GetEndpointCredentials("Codex Pool")
	if err != nil || len(credentials) != 1 {
		t.Fatalf("get credential: count=%d err=%v", len(credentials), err)
	}
	rateLimits, err := p.storage.GetCredentialRateLimits(credentials[0].ID)
	if err != nil {
		t.Fatalf("get captured rate limits: %v", err)
	}
	if rateLimits == nil || rateLimits.Data == nil || rateLimits.Data.Snapshot == nil || rateLimits.Data.Snapshot.Primary == nil {
		t.Fatalf("missing captured rate limits: %#v", rateLimits)
	}
	if got := rateLimits.Data.Snapshot.Primary.UsedPercent; got != 5 {
		t.Fatalf("captured primary used percent = %v, want 5", got)
	}
	if rateLimits.Data.Source != "sse" {
		t.Fatalf("rate limit source = %q, want sse", rateLimits.Data.Source)
	}
	for _, entry := range logger.GetLogger().GetLogs() {
		if strings.Contains(entry.Message, "retry_reason=client_canceled") {
			t.Fatalf("unexpected client cancellation log: %s", entry.Message)
		}
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

func TestCodexPendingCustomToolMissingCompletionIsRecovered(t *testing.T) {
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
	for _, want := range []string{
		`"type":"response.custom_tool_call_input.done"`,
		`"type":"response.output_item.done"`,
		`"type":"response.completed"`,
		`"input":"partial"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected recovered pending custom tool stream to contain %q, got %q", want, body)
		}
	}
	if strings.Contains(body, "event: error") || strings.Contains(body, streamFinishMissingResponsesDone) {
		t.Fatalf("did not expect missing-completion error for recovered pending tool, got %q", body)
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
