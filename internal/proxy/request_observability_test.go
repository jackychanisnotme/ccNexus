package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
)

func TestHandleProxyAddsRequestObservabilityHeadersAndLogs(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-test","usage":{"input_tokens":1,"output_tokens":2},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer upstream.Close()

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		{
			Name:        "EndpointA",
			APIUrl:      upstream.URL,
			APIKey:      "test-key",
			AuthMode:    config.AuthModeAPIKey,
			Enabled:     true,
			Transformer: "openai2",
			Model:       "gpt-5.5",
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

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`))
	req.RemoteAddr = "203.0.113.7:45678"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OpenClaw-Agent", "agent-alpha")
	req.Header.Set("X-ccNexus-Request-ID", "req-existing")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected upstream success, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-ccNexus-Request-ID"); got != "req-existing" {
		t.Fatalf("expected request id response header to preserve inbound id, got %q", got)
	}
	if got := rec.Header().Get("X-ccNexus-Endpoint"); got != "EndpointA" {
		t.Fatalf("expected endpoint response header, got %q", got)
	}
	if got := rec.Header().Get("X-ccNexus-Attempt"); got != "1" {
		t.Fatalf("expected attempt response header, got %q", got)
	}

	logs := logger.GetLogger().GetLogs()
	joined := ""
	for _, entry := range logs {
		joined += entry.Message + "\n"
	}
	for _, want := range []string{
		"request_id=req-existing",
		"client_ip=203.0.113.7",
		"agent=agent-alpha",
		"endpoint=EndpointA",
		"attempt=1",
		"upstream_status=200",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected logs to contain %q; logs:\n%s", want, joined)
		}
	}
}

func TestHandleProxyLogsRetryReasonAndFinalAttemptHeaders(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{\"error\":{\"message\":\"Too Many Requests\"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{\"id\":\"resp-test\",\"usage\":{\"input_tokens\":1,\"output_tokens\":2},\"output\":[]}`))
	}))
	defer upstream.Close()

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		{
			Name:        "EndpointA",
			APIUrl:      upstream.URL,
			APIKey:      "test-key",
			AuthMode:    config.AuthModeAPIKey,
			Enabled:     true,
			Transformer: "openai2",
			Model:       "gpt-5.5",
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

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ccNexus-Request-ID", "req-retry")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected retry to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if attempts != 2 {
		t.Fatalf("expected exactly 2 upstream attempts, got %d", attempts)
	}
	if got := rec.Header().Get("X-ccNexus-Attempt"); got != "2" {
		t.Fatalf("expected final attempt response header to be 2, got %q", got)
	}

	logs := logger.GetLogger().GetLogs()
	joined := ""
	for _, entry := range logs {
		joined += entry.Message + "\n"
	}
	for _, want := range []string{
		"request_id=req-retry",
		"attempt=1",
		"upstream_status=429",
		"retry_reason=rate_limited",
		"attempt=2",
		"upstream_status=200",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected logs to contain %q; logs:\n%s", want, joined)
		}
	}
}
