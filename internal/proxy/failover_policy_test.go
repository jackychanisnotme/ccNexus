package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
)

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
