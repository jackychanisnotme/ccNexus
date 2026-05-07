package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
)

func TestEnsureCodexResponsesPayload(t *testing.T) {
	raw := []byte(`{"model":"gpt-4.1","stream":true}`)
	out := ensureCodexResponsesPayload(raw)

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	store, ok := payload["store"].(bool)
	if !ok || store {
		t.Fatalf("expected store=false, got %#v", payload["store"])
	}
	stream, ok := payload["stream"].(bool)
	if !ok || !stream {
		t.Fatalf("expected stream=true, got %#v", payload["stream"])
	}
	if instructions, ok := payload["instructions"].(string); !ok || instructions != "" {
		t.Fatalf("expected instructions empty string, got %#v", payload["instructions"])
	}
}

func TestEnsureCodexResponsesPayloadOverridesStoreAndStream(t *testing.T) {
	raw := []byte(`{"model":"gpt-4.1","store":true}`)
	out := ensureCodexResponsesPayload(raw)

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	store, ok := payload["store"].(bool)
	if !ok || store {
		t.Fatalf("expected store=false, got %#v", payload["store"])
	}
	stream, ok := payload["stream"].(bool)
	if !ok || !stream {
		t.Fatalf("expected stream=true, got %#v", payload["stream"])
	}
}

func TestNormalizeTargetPathForBaseURLOnCodexBackend(t *testing.T) {
	got := normalizeTargetPathForBaseURL("https://chatgpt.com/backend-api/codex", "/v1/responses")
	if got != "/responses" {
		t.Fatalf("expected /responses, got %s", got)
	}
}

func TestNormalizeTargetPathForBaseURLAvoidsDuplicateV1(t *testing.T) {
	got := normalizeTargetPathForBaseURL("https://api.moonshot.ai/v1", "/v1/chat/completions")
	if got != "/chat/completions" {
		t.Fatalf("expected /chat/completions, got %s", got)
	}
}

func TestGetTargetPathUsesDeepSeekChatPath(t *testing.T) {
	ep := config.Endpoint{Transformer: "deepseek", APIUrl: "https://api.deepseek.com"}
	got := getTargetPath("/v1/messages", ep, []byte(`{}`), "cc_openai")
	if got != "/chat/completions" {
		t.Fatalf("expected /chat/completions, got %s", got)
	}
}

func TestGetTargetPathUsesV1ForCustomDeepSeekGateway(t *testing.T) {
	ep := config.Endpoint{Transformer: "deepseek", APIUrl: "https://gateway.example.com"}
	got := getTargetPath("/v1/responses", ep, []byte(`{}`), "cx_resp_openai")
	if got != "/v1/chat/completions" {
		t.Fatalf("expected /v1/chat/completions, got %s", got)
	}
}

func TestHandleProxyResponsesToCustomDeepSeekUsesV1ChatPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode upstream payload: %v", err)
		}
		if payload["model"] != "deepseek-v4-pro" {
			t.Fatalf("expected endpoint model override, got %#v", payload["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		{
			Name:        "DeepSeekGateway",
			APIUrl:      upstream.URL,
			APIKey:      "test-key",
			AuthMode:    config.AuthModeAPIKey,
			Enabled:     true,
			Transformer: "deepseek",
			Model:       "deepseek-v4-pro",
		},
	})

	p := &Proxy{
		config:         cfg,
		stats:          NewStats(&noopStatsStorage{}, "test-device"),
		httpClient:     upstream.Client(),
		activeRequests: make(map[string]int),
		endpointCtx:    make(map[string]context.Context),
		endpointCancel: make(map[string]context.CancelFunc),
		currentIndex:   0,
		resolver:       NewEndpointResolverWithFunc(cfg.GetEndpoints),
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d body=%q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"object":"response"`) {
		t.Fatalf("expected responses-format body, got %q", rec.Body.String())
	}
}

func TestHandleProxyChatToCustomDeepSeekUsesEndpointModel(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode upstream payload: %v", err)
		}
		if payload["model"] != "deepseek-v4-pro" {
			t.Fatalf("expected endpoint model override, got %#v", payload["model"])
		}
		if stream, ok := payload["stream"].(bool); !ok || !stream {
			t.Fatalf("expected stream=true to be preserved, got %#v", payload["stream"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		{
			Name:        "1052-2nd",
			APIUrl:      upstream.URL,
			APIKey:      "test-key",
			AuthMode:    config.AuthModeAPIKey,
			Enabled:     true,
			Transformer: "deepseek",
			Model:       "deepseek-v4-pro",
		},
	})

	p := &Proxy{
		config:         cfg,
		stats:          NewStats(&noopStatsStorage{}, "test-device"),
		httpClient:     upstream.Client(),
		activeRequests: make(map[string]int),
		endpointCtx:    make(map[string]context.Context),
		endpointCancel: make(map[string]context.CancelFunc),
		currentIndex:   0,
		resolver:       NewEndpointResolverWithFunc(cfg.GetEndpoints),
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.5","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d body=%q", rec.Code, rec.Body.String())
	}

	logs := logger.GetLogger().GetLogs()
	joined := ""
	for _, entry := range logs {
		joined += entry.Message + "\n"
	}
	for _, want := range []string{
		"Model mapping: client_model=gpt-5.5 upstream_model=deepseek-v4-pro",
		"Streaming deepseek-v4-pro",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected logs to contain %q; logs:\n%s", want, joined)
		}
	}
}

func TestHandleProxyEndpointSelectorModelSuffixDoesNotOverrideEndpointModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode upstream payload: %v", err)
		}
		if payload["model"] != "deepseek-v4-pro" {
			t.Fatalf("expected endpoint model to win over selector suffix, got %#v", payload["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		{
			Name:        "other",
			APIUrl:      "https://unused.example.com",
			APIKey:      "unused",
			AuthMode:    config.AuthModeAPIKey,
			Enabled:     true,
			Transformer: "openai",
			Model:       "gpt-4.1",
		},
		{
			Name:        "1052-2nd",
			APIUrl:      upstream.URL,
			APIKey:      "test-key",
			AuthMode:    config.AuthModeAPIKey,
			Enabled:     true,
			Transformer: "deepseek",
			Model:       "deepseek-v4-pro",
		},
	})

	p := &Proxy{
		config:         cfg,
		stats:          NewStats(&noopStatsStorage{}, "test-device"),
		httpClient:     upstream.Client(),
		activeRequests: make(map[string]int),
		endpointCtx:    make(map[string]context.Context),
		endpointCancel: make(map[string]context.CancelFunc),
		currentIndex:   0,
		resolver:       NewEndpointResolverWithFunc(cfg.GetEndpoints),
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"@1052-2nd/gpt-5.5","stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-ccNexus-Endpoint"); got != "1052-2nd" {
		t.Fatalf("expected request to select 1052-2nd, got %q", got)
	}
}

func TestOverrideModelInPayload(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.3-codex","stream":true}`)
	out := overrideModelInPayload(raw, "gpt-5.2-codex")

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if payload["model"] != "gpt-5.2-codex" {
		t.Fatalf("expected model override to gpt-5.2-codex, got %#v", payload["model"])
	}
}

func TestEnforceEndpointModelInPayloadSkipsGeminiBody(t *testing.T) {
	endpoint := config.Endpoint{Model: "gemini-2.0-flash"}
	raw := []byte(`{"contents":[]}`)
	out := enforceEndpointModelInPayload(raw, endpoint, "cx_chat_gemini")
	if string(out) != string(raw) {
		t.Fatalf("expected Gemini body to be unchanged, got %s", string(out))
	}
}

func TestExtractModelFromPayload(t *testing.T) {
	got := extractModelFromPayload([]byte(`{"model":"deepseek-v4-pro","messages":[]}`))
	if got != "deepseek-v4-pro" {
		t.Fatalf("expected deepseek-v4-pro, got %q", got)
	}
}

func TestValidateClientJSONRequestBody(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "object", body: `{"model":"gpt-5.5"}`},
		{name: "empty", body: ``, wantErr: true},
		{name: "whitespace", body: `   `, wantErr: true},
		{name: "truncated", body: `{"model":`, wantErr: true},
		{name: "array", body: `[]`, wantErr: true},
		{name: "null", body: `null`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateClientJSONRequestBody([]byte(tt.body))
			if tt.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("did not expect validation error: %v", err)
			}
		})
	}
}

func TestHandleProxyInvalidBodyLogIncludesMethodAndPath(t *testing.T) {
	logger.GetLogger().Clear()

	req := httptest.NewRequest(http.MethodPost, "/v1/responses?token=hidden", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	(&Proxy{}).handleProxy(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected HTTP 400, got %d", rec.Code)
	}

	for _, entry := range logger.GetLogger().GetLogs() {
		if !strings.Contains(entry.Message, "Invalid request body") {
			continue
		}
		if !strings.Contains(entry.Message, "method=POST") {
			t.Fatalf("expected invalid-body log to include method, got %q", entry.Message)
		}
		if !strings.Contains(entry.Message, "path=/v1/responses") {
			t.Fatalf("expected invalid-body log to include path, got %q", entry.Message)
		}
		if strings.Contains(entry.Message, "token=hidden") {
			t.Fatalf("expected invalid-body log to omit query string, got %q", entry.Message)
		}
		return
	}

	t.Fatal("expected invalid-body log entry")
}

func TestForceStreamInPayloadAddsChatUsageOptions(t *testing.T) {
	raw := []byte(`{"model":"gpt-4.1","messages":[{"role":"user","content":"hi"}]}`)
	out := forceStreamInPayload(raw)

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if stream, ok := payload["stream"].(bool); !ok || !stream {
		t.Fatalf("expected stream=true, got %#v", payload["stream"])
	}
	streamOptions, ok := payload["stream_options"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected stream_options object, got %#v", payload["stream_options"])
	}
	if includeUsage, ok := streamOptions["include_usage"].(bool); !ok || !includeUsage {
		t.Fatalf("expected include_usage=true, got %#v", streamOptions["include_usage"])
	}
}

func TestInjectEndpointThinkingInPayloadAddsResponsesReasoning(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.5","stream":true,"input":[]}`)
	out := injectEndpointThinkingInPayload(raw, "cx_resp_openai2", "High")

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	reasoning, ok := payload["reasoning"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected reasoning object, got %#v", payload["reasoning"])
	}
	if reasoning["effort"] != "high" {
		t.Fatalf("expected reasoning.effort=high, got %#v", reasoning["effort"])
	}
}

func TestInjectEndpointThinkingInPayloadAddsChatReasoningEffort(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.5","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	out := injectEndpointThinkingInPayload(raw, "cx_chat_openai", "xhigh")

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if payload["reasoning_effort"] != "xhigh" {
		t.Fatalf("expected reasoning_effort=xhigh, got %#v", payload["reasoning_effort"])
	}
	if _, ok := payload["reasoning"]; ok {
		t.Fatalf("did not expect responses reasoning on chat payload, got %#v", payload["reasoning"])
	}
}

func TestInjectEndpointThinkingInPayloadSkipsOff(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.5","stream":true,"input":[]}`)
	out := injectEndpointThinkingInPayload(raw, "cx_resp_openai2", "off")

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, ok := payload["reasoning"]; ok {
		t.Fatalf("did not expect reasoning when thinking is off, got %#v", payload["reasoning"])
	}
}

func TestShouldHandleAsStreamingResponseForCodexWithoutContentType(t *testing.T) {
	endpoint := config.Endpoint{
		Name:        "TokenPool",
		APIUrl:      "https://chatgpt.com/backend-api/codex",
		Transformer: "openai2",
	}
	if !shouldHandleAsStreamingResponse("", true, endpoint, "cx_chat_openai2") {
		t.Fatal("expected stream=true Codex response with empty content-type to be treated as streaming")
	}
	if shouldHandleAsStreamingResponse("", false, endpoint, "cx_chat_openai2") {
		t.Fatal("expected non-stream client request to not be treated as streaming when content-type is empty")
	}
	if !shouldHandleAsStreamingResponse("text/event-stream", false, endpoint, "cx_chat_openai2") {
		t.Fatal("expected text/event-stream content-type to be treated as streaming")
	}
}
