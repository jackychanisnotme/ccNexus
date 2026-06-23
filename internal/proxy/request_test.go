package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/storage"
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

func TestEnsureCodexResponsesPayloadRemovesMaxOutputTokens(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.5","max_output_tokens":2048,"input":"hello","metadata":{"source":"agent"}}`)
	out := ensureCodexResponsesPayload(raw)

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, ok := payload["max_output_tokens"]; ok {
		t.Fatalf("expected max_output_tokens to be removed, got %#v", payload["max_output_tokens"])
	}
	if payload["input"] != "hello" {
		t.Fatalf("expected input to be preserved, got %#v", payload["input"])
	}
	metadata, ok := payload["metadata"].(map[string]interface{})
	if !ok || metadata["source"] != "agent" {
		t.Fatalf("expected metadata to be preserved, got %#v", payload["metadata"])
	}
}

func TestCodexProxyUsesStableClientIdentity(t *testing.T) {
	endpoint := config.Endpoint{
		Name:        "Codex Pool",
		APIUrl:      config.CodexTokenPoolAPIURL,
		AuthMode:    config.AuthModeCodexTokenPool,
		Transformer: config.CodexTokenPoolTransformer,
		Model:       "gpt-5.5",
	}
	body := []byte(`{"model":"gpt-5.5","stream":true,"input":"ping"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
	credential := &storage.EndpointCredential{ProviderType: storage.ProviderTypeCodex}

	proxyReq, err := buildProxyRequest(req, endpoint, "test-token", body, "cx_resp_openai2", credential)
	if err != nil {
		t.Fatalf("buildProxyRequest failed: %v", err)
	}
	if got := proxyReq.Header.Get("Version"); got != "0.141.0" {
		t.Fatalf("Version = %q, want %q", got, "0.141.0")
	}
	if got := proxyReq.Header.Get("User-Agent"); !strings.Contains(got, "codex_cli_rs/0.141.0") {
		t.Fatalf("User-Agent = %q, want stable Codex identity", got)
	}
}

func TestBuildProxyRequestPreservesMaxOutputTokensForNonCodexResponses(t *testing.T) {
	endpoint := config.Endpoint{
		Name:        "OpenAI Responses",
		APIUrl:      "https://api.example.com",
		AuthMode:    config.AuthModeAPIKey,
		Transformer: "openai2",
		Model:       "gpt-5.5",
	}
	body := []byte(`{"model":"gpt-5.5","max_output_tokens":2048,"input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))

	proxyReq, err := buildProxyRequest(req, endpoint, "test-key", body, "cx_resp_openai2", nil)
	if err != nil {
		t.Fatalf("buildProxyRequest failed: %v", err)
	}
	defer proxyReq.Body.Close()
	upstreamBody, err := io.ReadAll(proxyReq.Body)
	if err != nil {
		t.Fatalf("read upstream body failed: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(upstreamBody, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if payload["max_output_tokens"] != float64(2048) {
		t.Fatalf("expected max_output_tokens to be preserved, got %#v", payload["max_output_tokens"])
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

func TestPrepareTransformerAcceptsPoeAsOpenAIChatCompatible(t *testing.T) {
	endpoint := config.Endpoint{
		Name:        "Poe",
		Transformer: "poe",
		Model:       "claude-fable-5",
	}

	trans, err := prepareTransformerForClient(ClientFormatClaude, endpoint)
	if err != nil {
		t.Fatalf("expected Poe transformer to prepare as OpenAI Chat compatible, got error: %v", err)
	}
	if got := trans.Name(); got != "cc_openai" {
		t.Fatalf("expected cc_openai transformer, got %s", got)
	}
}

func TestPrepareTransformerUsesPoeNativeResponsesForResponsesClient(t *testing.T) {
	endpoint := config.Endpoint{
		Name:        "Poe",
		Transformer: "poe",
		Model:       "claude-opus-4.8",
	}

	trans, err := prepareTransformerForClient(ClientFormatOpenAIResponses, endpoint)
	if err != nil {
		t.Fatalf("expected Poe transformer to prepare for Responses client, got error: %v", err)
	}
	if got := trans.Name(); got != "cx_resp_openai2" {
		t.Fatalf("expected Poe Responses client to use native Responses transformer, got %s", got)
	}
}

func TestBuildProxyRequestAdaptsPoeOpenAIChatPayload(t *testing.T) {
	endpoint := config.Endpoint{
		Name:        "Poe",
		APIUrl:      "https://api.poe.com/v1",
		AuthMode:    config.AuthModeAPIKey,
		Transformer: "poe",
		Model:       "claude-fable-5",
	}
	body := []byte(`{"model":"claude-fable-5","messages":[],"max_completion_tokens":8,"reasoning":{"effort":"xhigh"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")

	proxyReq, err := buildProxyRequest(req, endpoint, "poe-key", body, "cc_openai", nil)
	if err != nil {
		t.Fatalf("buildProxyRequest failed: %v", err)
	}
	if got := proxyReq.URL.String(); got != "https://api.poe.com/v1/chat/completions" {
		t.Fatalf("expected Poe chat completions URL, got %s", got)
	}
	if got := proxyReq.Header.Get("Authorization"); got != "Bearer poe-key" {
		t.Fatalf("expected Bearer auth header, got %q", got)
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(proxyReq.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode proxied payload: %v", err)
	}
	if payload["output_effort"] != "max" {
		t.Fatalf("expected output_effort=max, got %#v", payload["output_effort"])
	}
	if payload["max_tokens"].(float64) != 8 {
		t.Fatalf("expected max_tokens=8, got %#v", payload["max_tokens"])
	}
	if _, ok := payload["reasoning"]; ok {
		t.Fatalf("did not expect reasoning object for Poe, got %#v", payload["reasoning"])
	}
}

func TestBuildProxyRequestRoutesPoeResponsesToNativeResponsesPath(t *testing.T) {
	endpoint := config.Endpoint{
		Name:        "Poe",
		APIUrl:      "https://api.poe.com/v1",
		AuthMode:    config.AuthModeAPIKey,
		Transformer: "poe",
		Model:       "claude-opus-4.8",
	}
	body := []byte(`{"model":"claude-opus-4.8","stream":true,"input":"hi","reasoning":{"effort":"high"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")

	proxyReq, err := buildProxyRequest(req, endpoint, "poe-key", body, "cx_resp_openai2", nil)
	if err != nil {
		t.Fatalf("buildProxyRequest failed: %v", err)
	}
	if got := proxyReq.URL.String(); got != "https://api.poe.com/v1/responses" {
		t.Fatalf("expected Poe native Responses URL, got %s", got)
	}
	if got := proxyReq.Header.Get("Authorization"); got != "Bearer poe-key" {
		t.Fatalf("expected Bearer auth header, got %q", got)
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(proxyReq.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode proxied payload: %v", err)
	}
	if _, ok := payload["output_effort"]; ok {
		t.Fatalf("did not expect Chat-only output_effort on native Poe Responses payload, got %#v", payload["output_effort"])
	}
	if reasoning, ok := payload["reasoning"].(map[string]interface{}); !ok || reasoning["effort"] != "high" {
		t.Fatalf("expected native Responses reasoning effort to be preserved, got %#v", payload["reasoning"])
	}
}

func TestPoeEndpointIsNativeTierForResponsesClient(t *testing.T) {
	endpoint := config.Endpoint{
		Name:        "Poe",
		Transformer: "poe",
		Model:       "claude-opus-4.8",
	}

	if got := endpointClientFormatPreferenceTier(ClientFormatOpenAIResponses, endpoint); got != 0 {
		t.Fatalf("expected Poe to be native Responses tier for Responses clients, got %d", got)
	}
	if got := endpointClientFormatPreferenceTier(ClientFormatOpenAIChat, endpoint); got != 0 {
		t.Fatalf("expected Poe to remain native Chat tier for Chat clients, got %d", got)
	}
	if got := endpointClientFormatPreferenceTier(ClientFormatClaude, endpoint); got != 1 {
		t.Fatalf("expected Poe to remain bridge tier for Claude clients, got %d", got)
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
	if got := rec.Header().Get("X-AINexus-Endpoint"); got != "1052-2nd" {
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

func TestSendRequestDisablesClientTimeoutForStreamingBody(t *testing.T) {
	client := &http.Client{
		Timeout: 10 * time.Millisecond,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: &contextAwareDelayedBody{
					ctx:   req.Context(),
					delay: 30 * time.Millisecond,
					data:  []byte("data: ok\n\n"),
				},
				Request: req,
			}, nil
		}),
	}
	req, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/responses", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := sendRequestWithResponseHeaderTimeout(context.Background(), req, config.Endpoint{}, client, nil, 0, false)
	if err != nil {
		t.Fatalf("expected response with streaming client timeout disabled, got error: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("expected delayed streaming body to read successfully, got error: %v", err)
	}
	if string(body) != "data: ok\n\n" {
		t.Fatalf("unexpected body %q", body)
	}
}

func TestSendRequestKeepsClientTimeoutForNonStreamingBody(t *testing.T) {
	client := &http.Client{
		Timeout: 10 * time.Millisecond,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: &contextAwareDelayedBody{
					ctx:   req.Context(),
					delay: 30 * time.Millisecond,
					data:  []byte(`{"ok":true}`),
				},
				Request: req,
			}, nil
		}),
	}
	req, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/responses", strings.NewReader(`{"stream":false}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := sendRequestWithResponseHeaderTimeout(context.Background(), req, config.Endpoint{}, client, nil, 0, true)
	if err != nil {
		t.Fatalf("expected response headers before client timeout, got error: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatal("expected non-streaming body read to respect client timeout")
	}
}

type contextAwareDelayedBody struct {
	ctx    context.Context
	delay  time.Duration
	data   []byte
	offset int
	waited bool
}

func (b *contextAwareDelayedBody) Read(p []byte) (int, error) {
	if b.offset >= len(b.data) {
		return 0, io.EOF
	}
	if !b.waited {
		b.waited = true
		select {
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		case <-time.After(b.delay):
		}
	}
	n := copy(p, b.data[b.offset:])
	b.offset += n
	return n, nil
}

func (b *contextAwareDelayedBody) Close() error {
	return nil
}

func TestBuildProxyRequestForcesClaudeUserAgent(t *testing.T) {
	endpoint := config.Endpoint{
		Name:        "ClaudeEndpoint",
		APIUrl:      "https://claude.example.com",
		AuthMode:    config.AuthModeAPIKey,
		Transformer: "claude",
	}
	body := []byte(`{"model":"claude-opus-4-8","stream":true}`)

	for _, name := range []string{"cc_claude", "cx_chat_claude", "cx_resp_claude"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(body)))
		req.Header.Set("User-Agent", "OpenAI/Python_2.31.0")

		proxyReq, err := buildProxyRequest(req, endpoint, "test-key", body, name, nil)
		if err != nil {
			t.Fatalf("%s: buildProxyRequest failed: %v", name, err)
		}
		if got := proxyReq.Header.Get("User-Agent"); got != claudeUpstreamUserAgent {
			t.Fatalf("%s: expected User-Agent %q, got %q", name, claudeUpstreamUserAgent, got)
		}
		if got := proxyReq.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Fatalf("%s: expected anthropic-version set, got %q", name, got)
		}
	}
}

func TestBuildProxyRequestUsesBearerOnlyForClaudeOAuthCredential(t *testing.T) {
	endpoint := config.Endpoint{
		Name:        "ClaudeOAuth",
		APIUrl:      "https://api.anthropic.com",
		AuthMode:    config.AuthModeClaudeOAuthTokenPool,
		Transformer: "claude",
		Model:       "opus",
	}
	body := []byte(`{"model":"opus","messages":[],"max_tokens":16}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(body)))
	req.Header.Set("User-Agent", "OpenAI/JS")

	proxyReq, err := buildProxyRequest(req, endpoint, "claude-oauth-token", body, "cc_claude", &storage.EndpointCredential{
		ProviderType: "claude_oauth",
		AccessToken:  "claude-oauth-token",
	})
	if err != nil {
		t.Fatalf("buildProxyRequest failed: %v", err)
	}
	if got := proxyReq.Header.Get("Authorization"); got != "Bearer claude-oauth-token" {
		t.Fatalf("Authorization = %q, want bearer OAuth token", got)
	}
	if got := proxyReq.Header.Get("x-api-key"); got != "" {
		t.Fatalf("expected x-api-key to be omitted for Claude OAuth, got %q", got)
	}
	if got := proxyReq.Header.Get("anthropic-version"); got != "2023-06-01" {
		t.Fatalf("anthropic-version = %q, want 2023-06-01", got)
	}
}

func TestResolveProxyURLForRequestPrefersEndpointProxy(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateProxy(&config.ProxyConfig{URL: "http://127.0.0.1:7890"})
	cfg.UpdateCodexProxy(&config.ProxyConfig{URL: "http://127.0.0.1:7891"})

	got := resolveProxyURLForRequest(cfg, mustParseURL(t, "https://api.example.com/v1/chat/completions"), config.Endpoint{ProxyURL: "http://127.0.0.1:7892"})
	if got != "http://127.0.0.1:7892" {
		t.Fatalf("expected endpoint proxy to win, got %q", got)
	}
}

func TestResolveProxyURLForRequestUsesCodexProxyForCodexRequests(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateProxy(&config.ProxyConfig{URL: "http://127.0.0.1:7890"})
	cfg.UpdateCodexProxy(&config.ProxyConfig{URL: "http://127.0.0.1:7891"})

	got := resolveProxyURLForRequest(cfg, mustParseURL(t, "https://chatgpt.com/backend-api/codex"), config.Endpoint{})
	if got != "http://127.0.0.1:7891" {
		t.Fatalf("expected codex proxy fallback, got %q", got)
	}
}

func TestResolveProxyURLForRequestFallsBackToGlobalProxy(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateProxy(&config.ProxyConfig{URL: "http://127.0.0.1:7890"})

	got := resolveProxyURLForRequest(cfg, mustParseURL(t, "https://api.example.com/v1/chat/completions"), config.Endpoint{})
	if got != "http://127.0.0.1:7890" {
		t.Fatalf("expected global proxy fallback, got %q", got)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	return parsed
}
