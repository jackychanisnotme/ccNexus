package proxy

import (
	"context"
	"fmt"
	"net"
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

func TestRetryReasonForHTTPStatusClassifiesRouteUnavailable(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{
			name:       "503 no available channel",
			statusCode: http.StatusServiceUnavailable,
			body:       hermesRouteUnavailableBody,
			want:       "route_unavailable",
		},
		{
			name:       "429 no available route",
			statusCode: http.StatusTooManyRequests,
			body:       `{"error":{"message":"No available route for requested model"}}`,
			want:       "route_unavailable",
		},
		{
			name:       "403 model unavailable",
			statusCode: http.StatusForbidden,
			body:       `{"error":{"message":"Model unavailable for this distributor"}}`,
			want:       "route_unavailable",
		},
		{
			name:       "404 distributor group unavailable",
			statusCode: http.StatusNotFound,
			body:       `{"error":{"message":"No route under group plus (distributor)"}}`,
			want:       "route_unavailable",
		},
		{
			name:       "generic 503 stays upstream 5xx",
			statusCode: http.StatusServiceUnavailable,
			body:       `{"error":{"message":"Service temporarily unavailable"}}`,
			want:       "upstream_5xx",
		},
		{
			name:       "generic 429 stays rate limited",
			statusCode: http.StatusTooManyRequests,
			body:       `{"error":{"message":"Rate limit exceeded"}}`,
			want:       "rate_limited",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryReasonForHTTPStatus(tt.statusCode, tt.body); got != tt.want {
				t.Fatalf("expected retry reason %q, got %q", tt.want, got)
			}
		})
	}
}

func TestRouteUnavailableUsesFastFailoverWhileGeneric503StaysSlow(t *testing.T) {
	if got := failoverAttemptsForHTTPFailure(http.StatusServiceUnavailable, hermesRouteUnavailableBody); got != 1 {
		t.Fatalf("expected route-unavailable failure to fail over after one attempt, got %d", got)
	}
	if got := failoverAttemptsForHTTPFailure(http.StatusServiceUnavailable, `{"error":{"message":"Service temporarily unavailable"}}`); got != endpointSlowFailoverAttempts {
		t.Fatalf("expected generic 503 to keep slow failover, got %d attempts", got)
	}
}

func TestHTTPRetryableStatusUsesSlowEndpointFailover(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "application/json")
		if primaryHits <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"Too Many Requests"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"resp-primary","usage":{"input_tokens":1,"output_tokens":2},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer primary.Close()

	var fallbackHits int
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-fallback","usage":{"input_tokens":1,"output_tokens":2},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer fallback.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", primary.URL),
		failoverPolicyTestEndpoint("Fallback", fallback.URL),
	}, primary.Client())

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-slow-failover")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected request to succeed on primary after slow retry, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if primaryHits != 3 {
		t.Fatalf("expected slow HTTP failover to try primary 3 times before rotating, got %d", primaryHits)
	}
	if fallbackHits != 0 {
		t.Fatalf("expected fallback endpoint not to be used before 3 HTTP failures, got %d hits", fallbackHits)
	}
	if got := rec.Header().Get(headerCCNexusEndpoint); got != "Primary" {
		t.Fatalf("expected final endpoint header Primary, got %q", got)
	}
	if got := rec.Header().Get(headerCCNexusAttempt); got != "3" {
		t.Fatalf("expected final attempt header 3, got %q", got)
	}
}

func TestQuotaExhaustedUsesImmediateRequestLocalFailoverWithoutBackoff(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"用户额度不足, 剩余额度: ＄0.000000","type":"new_api_error","code":"insufficient_user_quota"}}`))
	}))
	defer primary.Close()

	var fallbackHits int
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-fallback","usage":{"input_tokens":1,"output_tokens":2},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer fallback.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", primary.URL),
		failoverPolicyTestEndpoint("Fallback", fallback.URL),
	}, primary.Client())
	var sleeps []time.Duration
	p.retrySleep = func(d time.Duration) {
		sleeps = append(sleeps, d)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-quota-exhausted")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected request to immediately fail over after quota exhaustion, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if primaryHits != 1 {
		t.Fatalf("expected quota exhausted endpoint to be tried once, got %d", primaryHits)
	}
	if fallbackHits != 1 {
		t.Fatalf("expected fallback endpoint to be used once, got %d", fallbackHits)
	}
	if len(sleeps) != 0 {
		t.Fatalf("expected no backoff sleeps for quota exhaustion, got %v", sleeps)
	}
	if got := rec.Header().Get(headerCCNexusEndpoint); got != "Fallback" {
		t.Fatalf("expected final endpoint header Fallback, got %q", got)
	}
	if got := rec.Header().Get(headerCCNexusAttempt); got != "2" {
		t.Fatalf("expected quota exhausted fallback on second overall attempt, got attempt header %q", got)
	}
	if got := p.GetCurrentEndpointName(); got != "Primary" {
		t.Fatalf("expected global current endpoint to remain Primary, got %q", got)
	}

	logs := joinedProxyLogs()
	for _, want := range []string{
		"request_id=req-quota-exhausted",
		"retry_reason=quota_exhausted",
		"failover_scope=request_local",
		"failover_reason=quota_exhausted",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %q, got logs:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "Backing off before retry") {
		t.Fatalf("expected quota exhaustion not to back off, got logs:\n%s", logs)
	}
	if strings.Contains(logs, "[SWITCH]") {
		t.Fatalf("expected no global switch log during quota failover, got logs:\n%s", logs)
	}
}

func TestAPIKeyUnauthorizedUsesImmediateRequestLocalFailover(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid token","type":"new_api_error","code":""}}`))
	}))
	defer primary.Close()

	var fallbackHits int
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validResponsesBody("resp-fallback", "ok")))
	}))
	defer fallback.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", primary.URL),
		failoverPolicyTestEndpoint("Fallback", fallback.URL),
	}, primary.Client())

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-api-key-auth-failover")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected API key auth failure to fail over and succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if primaryHits != 1 {
		t.Fatalf("expected primary to be tried once for endpoint auth failure, got %d", primaryHits)
	}
	if fallbackHits != 1 {
		t.Fatalf("expected fallback endpoint to be used once, got %d", fallbackHits)
	}
	if got := rec.Header().Get(headerCCNexusEndpoint); got != "Fallback" {
		t.Fatalf("expected final endpoint header Fallback, got %q", got)
	}
	if got := rec.Header().Get(headerCCNexusAttempt); got != "2" {
		t.Fatalf("expected auth failure fallback on second overall attempt, got attempt header %q", got)
	}
	if got := p.GetCurrentEndpointName(); got != "Primary" {
		t.Fatalf("expected request-local failover to keep global current endpoint Primary, got %q", got)
	}
	p.cooldownMu.RLock()
	cooldown, cooled := p.endpointCooldowns["Primary"]
	p.cooldownMu.RUnlock()
	if !cooled || cooldown.Reason != retryReasonEndpointAuthFailed {
		t.Fatalf("expected Primary cooldown for endpoint auth failure, got cooled=%v cooldown=%#v", cooled, cooldown)
	}

	logs := joinedProxyLogs()
	for _, want := range []string{
		"request_id=req-api-key-auth-failover",
		"retry_reason=endpoint_auth_failed",
		"failover_scope=request_local",
		"failover_reason=endpoint_auth_failed",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %q, got logs:\n%s", want, logs)
		}
	}
}

func TestAPIKeyForbiddenAuthFailureUsesRequestLocalFailover(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"authentication_error"}}`))
	}))
	defer primary.Close()

	var fallbackHits int
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validResponsesBody("resp-fallback", "ok")))
	}))
	defer fallback.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", primary.URL),
		failoverPolicyTestEndpoint("Fallback", fallback.URL),
	}, primary.Client())

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-api-key-forbidden-auth-failover")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 403 API key auth failure to fail over and succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if primaryHits != 1 || fallbackHits != 1 {
		t.Fatalf("expected one primary hit and one fallback hit, got primary=%d fallback=%d", primaryHits, fallbackHits)
	}
	if logs := joinedProxyLogs(); !strings.Contains(logs, "retry_reason=endpoint_auth_failed") || !strings.Contains(logs, "failover_scope=request_local") {
		t.Fatalf("expected endpoint auth failover logs, got logs:\n%s", logs)
	}
}

func TestAPIKeyUnauthorizedPinnedEndpointDoesNotFailover(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid token"}}`))
	}))
	defer primary.Close()

	var fallbackHits int
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validResponsesBody("resp-fallback", "ok")))
	}))
	defer fallback.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", primary.URL),
		failoverPolicyTestEndpoint("Fallback", fallback.URL),
	}, primary.Client())

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CCN-Endpoint", "Primary")
	req.Header.Set(headerCCNexusRequestID, "req-api-key-auth-pinned")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected pinned endpoint auth failure to return 401, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if primaryHits != 1 {
		t.Fatalf("expected pinned primary to be tried once, got %d", primaryHits)
	}
	if fallbackHits != 0 {
		t.Fatalf("expected pinned endpoint not to fail over, got fallback hits=%d", fallbackHits)
	}
	logs := joinedProxyLogs()
	if !strings.Contains(logs, "retry_reason=endpoint_auth_failed") {
		t.Fatalf("expected endpoint auth failure log, got logs:\n%s", logs)
	}
	if strings.Contains(logs, "failover_scope=request_local") {
		t.Fatalf("did not expect request-local failover for pinned endpoint, got logs:\n%s", logs)
	}
}

func TestTokenPoolUnauthorizedStillRetriesNextToken(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var tokens []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		tokens = append(tokens, token)
		w.Header().Set("Content-Type", "application/json")
		if token == "token-a" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid token","type":"new_api_error","code":""}}`))
			return
		}
		_, _ = w.Write([]byte(validResponsesBody("resp-ok", "ok")))
	}))
	defer upstream.Close()

	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
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

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-token-pool-401-next-token")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected token pool auth failure to retry next token and succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if strings.Join(tokens, ",") != "token-a,token-b" {
		t.Fatalf("expected retry to move from token-a to token-b, got tokens=%v", tokens)
	}
	updatedA, err := store.GetCredentialByID(credA.ID)
	if err != nil {
		t.Fatalf("load cred A: %v", err)
	}
	if updatedA == nil || updatedA.Status != "invalid" {
		t.Fatalf("expected first token to be invalidated, got %#v", updatedA)
	}
	logs := joinedProxyLogs()
	if !strings.Contains(logs, "retry_reason=credential_auth_failed") {
		t.Fatalf("expected token-pool credential auth failure logs, got logs:\n%s", logs)
	}
	if strings.Contains(logs, "retry_reason=endpoint_auth_failed") {
		t.Fatalf("did not expect API-key endpoint auth failure branch for token pool, got logs:\n%s", logs)
	}
}

func TestClaudeOAuthTokenPoolUsesOnlyClaudeOAuthCredentials(t *testing.T) {
	var tokens []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		tokens = append(tokens, token)
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("expected Claude OAuth upstream path /v1/messages, got %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "" {
			t.Fatalf("expected no x-api-key for Claude OAuth token, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if token == "claude-token-a" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"OAuth token revoked"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"msg-claude","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"opus","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`))
	}))
	defer upstream.Close()

	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	wrongProvider := storage.EndpointCredential{EndpointName: "ClaudeOAuth", ProviderType: "codex", AccessToken: "codex-token", Enabled: true}
	credA := storage.EndpointCredential{EndpointName: "ClaudeOAuth", ProviderType: "claude_oauth", AccessToken: "claude-token-a", Enabled: true}
	credB := storage.EndpointCredential{EndpointName: "ClaudeOAuth", ProviderType: "claude_oauth", AccessToken: "claude-token-b", Enabled: true}
	for _, cred := range []*storage.EndpointCredential{&wrongProvider, &credA, &credB} {
		if err := store.SaveEndpointCredential(cred); err != nil {
			t.Fatalf("save credential %#v: %v", cred, err)
		}
	}

	cfg := config.DefaultConfig()
	endpoint := config.Endpoint{
		Name:        "ClaudeOAuth",
		APIUrl:      upstream.URL,
		AuthMode:    config.AuthModeClaudeOAuthTokenPool,
		Enabled:     true,
		Transformer: "claude",
		Model:       "opus",
	}
	cfg.UpdateEndpoints([]config.Endpoint{endpoint})
	p := New(cfg, &noopStatsStorage{}, store, "test-device")
	p.httpClient = upstream.Client()
	p.retrySleep = func(time.Duration) {}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-5-20250929","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected Claude OAuth retry to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if strings.Join(tokens, ",") != "claude-token-a,claude-token-b" {
		t.Fatalf("expected only Claude OAuth credentials to be used, got tokens=%v", tokens)
	}
	updatedA, err := store.GetCredentialByID(credA.ID)
	if err != nil {
		t.Fatalf("load first Claude credential: %v", err)
	}
	if updatedA == nil || updatedA.Status != "invalid" {
		t.Fatalf("expected first Claude OAuth credential to be invalidated, got %#v", updatedA)
	}
}

func TestConnectionFailureUsesFastEndpointFailover(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	badURL := closedLocalHTTPURL(t)
	var fallbackHits int
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-fallback","usage":{"input_tokens":1,"output_tokens":2},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer fallback.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", badURL),
		failoverPolicyTestEndpoint("Fallback", fallback.URL),
	}, fallback.Client())

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-fast-failover")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected request to succeed on fallback after fast connection failover, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if fallbackHits != 1 {
		t.Fatalf("expected fallback endpoint to be used once, got %d hits", fallbackHits)
	}
	if got := rec.Header().Get(headerCCNexusEndpoint); got != "Fallback" {
		t.Fatalf("expected final endpoint header Fallback, got %q", got)
	}
	if got := rec.Header().Get(headerCCNexusAttempt); got != "3" {
		t.Fatalf("expected connection failure to rotate on third overall attempt, got attempt header %q", got)
	}
}

func newFailoverPolicyTestProxy(endpoints []config.Endpoint, client *http.Client) *Proxy {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints(endpoints)
	return &Proxy{
		config:                  cfg,
		configEndpointsSnapshot: cloneEndpoints(cfg.GetEndpoints()),
		stats:                   NewStats(&noopStatsStorage{}, "test-device"),
		httpClient:              client,
		activeRequests:          make(map[string]int),
		endpointCtx:             make(map[string]context.Context),
		endpointCancel:          make(map[string]context.CancelFunc),
		currentIndex:            0,
		resolver:                NewEndpointResolverWithFunc(cfg.GetEndpoints),
		retrySleep:              func(time.Duration) {},
		endpointCooldowns:       make(map[string]endpointCooldown),
		endpointCircuitBreakers: make(map[string]*endpointCircuitBreakerState),
	}
}

func failoverPolicyTestEndpoint(name, apiURL string) config.Endpoint {
	return config.Endpoint{
		Name:        name,
		APIUrl:      apiURL,
		APIKey:      "test-key",
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "openai2",
		Model:       "gpt-5.5",
	}
}

func closedLocalHTTPURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate local TCP port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("failed to close local TCP listener: %v", err)
	}
	return fmt.Sprintf("http://%s", addr)
}
