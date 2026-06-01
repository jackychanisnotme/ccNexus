package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestHTTPFailureFallbackIsRequestLocalAndDoesNotSwitchGlobalEndpoint(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"Too Many Requests"}}`))
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
	var runtimeEvents []EndpointRuntimeEvent
	p.SetOnEndpointRuntimeChanged(func(event EndpointRuntimeEvent) {
		runtimeEvents = append(runtimeEvents, event)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-local-fallback")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected request-local fallback to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if primaryHits != endpointSlowFailoverAttempts {
		t.Fatalf("expected primary to be tried %d times before request-local fallback, got %d", endpointSlowFailoverAttempts, primaryHits)
	}
	if fallbackHits != 1 {
		t.Fatalf("expected fallback to be hit once, got %d", fallbackHits)
	}
	if got := rec.Header().Get(headerCCNexusEndpoint); got != "Fallback" {
		t.Fatalf("expected response endpoint header Fallback, got %q", got)
	}
	if got := p.GetCurrentEndpointName(); got != "Primary" {
		t.Fatalf("expected global current endpoint to remain Primary, got %q", got)
	}
	if !hasRuntimeFailureEvent(runtimeEvents, "Primary", "rate_limited", http.StatusInternalServerError) {
		t.Fatalf("expected Primary failure runtime event, got %#v", runtimeEvents)
	}
	if !hasRuntimeSuccessEvent(runtimeEvents, "Fallback") {
		t.Fatalf("expected Fallback success runtime event, got %#v", runtimeEvents)
	}

	logs := joinedProxyLogs()
	if !strings.Contains(logs, "failover_scope=request_local") {
		t.Fatalf("expected request-local failover log, got logs:\n%s", logs)
	}
	if strings.Contains(logs, "[SWITCH]") {
		t.Fatalf("expected no global switch log during request-local fallback, got logs:\n%s", logs)
	}
}

func TestResponsesRequestUsesManualClaudeDefaultThenFailsOverToGPT(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var claudeHits int
	claudeEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claudeHits++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"claude unavailable"}}`))
	}))
	defer claudeEndpoint.Close()

	var gptHits int
	gptEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gptHits++
		if r.URL.Path != "/v1/responses" {
			t.Errorf("expected GPT endpoint to receive /v1/responses, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-gpt","usage":{"input_tokens":1,"output_tokens":2},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer gptEndpoint.Close()

	currentClaude := failoverPolicyTestEndpoint("Current Claude", claudeEndpoint.URL)
	currentClaude.Transformer = "claude"
	currentClaude.Model = "claude-opus-4-7"
	nativeGPT := failoverPolicyTestEndpoint("Native GPT", gptEndpoint.URL)

	// Claude is listed first, so it is the manually-selected default (currentIndex 0).
	p := newFailoverPolicyTestProxy([]config.Endpoint{
		currentClaude,
		nativeGPT,
	}, claudeEndpoint.Client())

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-claude-default-failover")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected failover to GPT to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	// Default Claude must be attempted first (proves the manual default wins over format).
	if claudeHits == 0 {
		t.Fatalf("expected manual Claude default to be tried first, hits=%d", claudeHits)
	}
	// And the request must still succeed by failing over to the GPT endpoint.
	if gptHits != 1 {
		t.Fatalf("expected failover to native GPT endpoint once, got %d", gptHits)
	}
	if got := rec.Header().Get(headerCCNexusEndpoint); got != "Native GPT" {
		t.Fatalf("expected final response endpoint Native GPT, got %q", got)
	}
	if !strings.Contains(joinedProxyLogs(), "failover_scope=request_local") {
		t.Fatalf("expected request-local failover log, got logs:\n%s", joinedProxyLogs())
	}
}

func TestResponsesRequestUsesManualGPTDefaultThenFailsOverToClaude(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var gptHits int
	gptEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gptHits++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"gpt unavailable"}}`))
	}))
	defer gptEndpoint.Close()

	var claudeHits int
	claudeEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claudeHits++
		if r.URL.Path != "/v1/messages" {
			t.Errorf("expected Claude endpoint to receive /v1/messages, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg-claude","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-opus-4-7","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`))
	}))
	defer claudeEndpoint.Close()

	currentGPT := failoverPolicyTestEndpoint("Current GPT", gptEndpoint.URL)
	fallbackClaude := failoverPolicyTestEndpoint("Fallback Claude", claudeEndpoint.URL)
	fallbackClaude.Transformer = "claude"
	fallbackClaude.Model = "claude-opus-4-7"

	// GPT is listed first, so it is the manually-selected default (currentIndex 0).
	p := newFailoverPolicyTestProxy([]config.Endpoint{
		currentGPT,
		fallbackClaude,
	}, gptEndpoint.Client())

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-gpt-default-failover")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected failover to Claude to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if gptHits == 0 {
		t.Fatalf("expected manual GPT default to be tried first, hits=%d", gptHits)
	}
	if claudeHits != 1 {
		t.Fatalf("expected failover to Claude endpoint once, got %d", claudeHits)
	}
	if got := rec.Header().Get(headerCCNexusEndpoint); got != "Fallback Claude" {
		t.Fatalf("expected final response endpoint Fallback Claude, got %q", got)
	}
	if !strings.Contains(joinedProxyLogs(), "failover_scope=request_local") {
		t.Fatalf("expected request-local failover log, got logs:\n%s", joinedProxyLogs())
	}
}

func TestResponsesRequestUsesClaudeEndpointWhenNoNativeResponsesEndpoint(t *testing.T) {
	var claudeHits int
	claudeEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claudeHits++
		if r.URL.Path != "/v1/messages" {
			t.Errorf("expected Claude endpoint to receive /v1/messages, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg-claude","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-opus-4-7","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`))
	}))
	defer claudeEndpoint.Close()

	onlyClaude := failoverPolicyTestEndpoint("Only Claude", claudeEndpoint.URL)
	onlyClaude.Transformer = "claude"
	onlyClaude.Model = "claude-opus-4-7"

	p := newFailoverPolicyTestProxy([]config.Endpoint{onlyClaude}, claudeEndpoint.Client())

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-only-claude")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected single Claude endpoint to remain usable, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if claudeHits != 1 {
		t.Fatalf("expected Claude endpoint to be hit once, got %d", claudeHits)
	}
	if got := rec.Header().Get(headerCCNexusEndpoint); got != "Only Claude" {
		t.Fatalf("expected response endpoint Only Claude, got %q", got)
	}
	if !strings.Contains(rec.Body.String(), `"object":"response"`) {
		t.Fatalf("expected Claude response to be converted to Responses shape, got %q", rec.Body.String())
	}
}

func TestCircuitBreakerConsecutiveFailuresLongCooldownAndFastFallback(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream unavailable"}}`))
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
	p.config.UpdateFailover(&config.FailoverConfig{
		RecoveredEndpointPolicy: config.RecoveredEndpointPolicyAutoReturn,
		Cooldowns:               config.DefaultFailoverConfig().Cooldowns,
		CircuitBreaker: &config.FailoverCircuitBreakerConfig{
			ConsecutiveFailures:  1,
			WindowSec:            60,
			FailureRateThreshold: 0.90,
			MinRequests:          10,
			CooldownSec:          600,
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-circuit-consecutive")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected fallback success after circuit breaker, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if primaryHits != 1 {
		t.Fatalf("expected circuit breaker to stop retrying Primary after one failure, got %d hits", primaryHits)
	}
	if fallbackHits != 1 {
		t.Fatalf("expected Fallback to be hit once, got %d", fallbackHits)
	}

	p.cooldownMu.RLock()
	cooldown, cooled := p.endpointCooldowns["Primary"]
	p.cooldownMu.RUnlock()
	if !cooled || cooldown.Reason != retryReasonCircuitBreakerConsecutive {
		t.Fatalf("expected Primary long cooldown from circuit breaker, got cooled=%v cooldown=%#v", cooled, cooldown)
	}
	if remaining := time.Until(cooldown.Until); remaining < 9*time.Minute {
		t.Fatalf("expected long cooldown near 10m, got remaining=%s", remaining)
	}

	logs := joinedProxyLogs()
	if !strings.Contains(logs, "[CIRCUIT_BREAKER]") ||
		!strings.Contains(logs, "trigger=circuit_breaker_consecutive_failures") ||
		!strings.Contains(logs, "Keeping existing cooldown") {
		t.Fatalf("expected circuit breaker and long-cooldown preservation logs, got:\n%s", logs)
	}
}

func TestCircuitBreakerFailureRateTriggersLongCooldown(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", "https://primary.example"),
	}, nil)
	p.config.UpdateFailover(&config.FailoverConfig{
		RecoveredEndpointPolicy: config.RecoveredEndpointPolicyAutoReturn,
		Cooldowns:               config.DefaultFailoverConfig().Cooldowns,
		CircuitBreaker: &config.FailoverCircuitBreakerConfig{
			ConsecutiveFailures:  0,
			WindowSec:            60,
			FailureRateThreshold: 0.50,
			MinRequests:          4,
			CooldownSec:          600,
		},
	})

	p.recordEndpointSuccessEvent("Primary")
	p.recordEndpointError("Primary", "upstream_5xx", http.StatusServiceUnavailable)
	p.recordEndpointSuccessEvent("Primary")
	if p.isEndpointInActiveCooldown("Primary") {
		t.Fatal("did not expect cooldown before reaching minimum window request count")
	}

	p.recordEndpointError("Primary", "upstream_5xx", http.StatusServiceUnavailable)

	p.cooldownMu.RLock()
	cooldown, cooled := p.endpointCooldowns["Primary"]
	p.cooldownMu.RUnlock()
	if !cooled || cooldown.Reason != retryReasonCircuitBreakerFailureRate {
		t.Fatalf("expected failure-rate circuit cooldown, got cooled=%v cooldown=%#v", cooled, cooldown)
	}
	if !strings.Contains(joinedProxyLogs(), "trigger=circuit_breaker_failure_rate") {
		t.Fatalf("expected failure-rate circuit log, got logs:\n%s", joinedProxyLogs())
	}
}

func hasRuntimeFailureEvent(events []EndpointRuntimeEvent, endpointName, reason string, statusCode int) bool {
	for _, event := range events {
		if event.Event == "failure" &&
			event.EndpointName == endpointName &&
			event.LastFailureAt != nil &&
			event.LastFailureReason == reason &&
			event.LastFailureStatusCode == statusCode {
			return true
		}
	}
	return false
}

func hasRuntimeSuccessEvent(events []EndpointRuntimeEvent, endpointName string) bool {
	for _, event := range events {
		if event.Event == "success" &&
			event.EndpointName == endpointName &&
			event.LastSuccessAt != nil {
			return true
		}
	}
	return false
}

func TestEndpointRuntimeEventClearsStatusCodeForNonHTTPFailure(t *testing.T) {
	p := &Proxy{
		stats:          NewStats(&noopStatsStorage{}, "test-device"),
		activeRequests: make(map[string]int),
	}
	var runtimeEvents []EndpointRuntimeEvent
	p.SetOnEndpointRuntimeChanged(func(event EndpointRuntimeEvent) {
		runtimeEvents = append(runtimeEvents, event)
	})

	p.recordEndpointError("Primary", "rate_limited", http.StatusTooManyRequests)
	p.recordEndpointError("Primary", "transient_network_error")

	if len(runtimeEvents) != 2 {
		t.Fatalf("expected two runtime events, got %#v", runtimeEvents)
	}
	if runtimeEvents[0].LastFailureReason != "rate_limited" || runtimeEvents[0].LastFailureStatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected HTTP failure status on first event, got %#v", runtimeEvents[0])
	}
	if runtimeEvents[1].LastFailureReason != "transient_network_error" || runtimeEvents[1].LastFailureStatusCode != 0 {
		t.Fatalf("expected non-http failure to clear status code, got %#v", runtimeEvents[1])
	}
}

func TestRequestLocalFallbackDoesNotAffectNextRequest(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	primaryHitsByRequest := map[string]int{}
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(headerCCNexusRequestID)
		primaryHitsByRequest[requestID]++
		w.Header().Set("Content-Type", "application/json")
		if requestID == "req-a" {
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
	disableEndpointCooldownsForTest(p)

	recA := issueFailoverPolicyTestRequest(p, "req-a")
	if recA.Code != http.StatusOK {
		t.Fatalf("expected req-a to succeed via request-local fallback, got status=%d body=%q", recA.Code, recA.Body.String())
	}
	if got := recA.Header().Get(headerCCNexusEndpoint); got != "Fallback" {
		t.Fatalf("expected req-a to complete on Fallback, got %q", got)
	}

	recB := issueFailoverPolicyTestRequest(p, "req-b")
	if recB.Code != http.StatusOK {
		t.Fatalf("expected req-b to succeed, got status=%d body=%q", recB.Code, recB.Body.String())
	}
	if got := recB.Header().Get(headerCCNexusEndpoint); got != "Primary" {
		t.Fatalf("expected req-b to still start on Primary after req-a fallback, got %q", got)
	}
	if got := primaryHitsByRequest["req-b"]; got != 1 {
		t.Fatalf("expected req-b to hit Primary once, got %d", got)
	}
	if got := p.GetCurrentEndpointName(); got != "Primary" {
		t.Fatalf("expected global current endpoint to remain Primary, got %q", got)
	}
	if fallbackHits != 1 {
		t.Fatalf("expected only req-a to use fallback once, got fallback hits=%d", fallbackHits)
	}
}

func TestConcurrentRequestLocalFallbackDoesNotAffectOtherRequest(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var mu sync.Mutex
	primaryHitsByRequest := map[string]int{}
	fallbackHitsByRequest := map[string]int{}
	fallbackStarted := make(chan struct{})
	releaseFallback := make(chan struct{})
	var fallbackStartedOnce sync.Once
	var releaseFallbackOnce sync.Once
	t.Cleanup(func() {
		releaseFallbackOnce.Do(func() { close(releaseFallback) })
	})

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(headerCCNexusRequestID)
		mu.Lock()
		primaryHitsByRequest[requestID]++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if requestID == "req-a-concurrent" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{\"error\":{\"message\":\"Too Many Requests\"}}`))
			return
		}
		_, _ = w.Write([]byte(`{\"id\":\"resp-primary\",\"usage\":{\"input_tokens\":1,\"output_tokens\":2},\"output\":[]}`))
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(headerCCNexusRequestID)
		mu.Lock()
		fallbackHitsByRequest[requestID]++
		mu.Unlock()

		if requestID == "req-a-concurrent" {
			fallbackStartedOnce.Do(func() { close(fallbackStarted) })
			select {
			case <-releaseFallback:
			case <-time.After(3 * time.Second):
				http.Error(w, "timed out waiting to release fallback response", http.StatusGatewayTimeout)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{\"id\":\"resp-fallback\",\"usage\":{\"input_tokens\":1,\"output_tokens\":2},\"output\":[]}`))
	}))
	defer fallback.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", primary.URL),
		failoverPolicyTestEndpoint("Fallback", fallback.URL),
	}, primary.Client())
	disableEndpointCooldownsForTest(p)
	p.retrySleep = func(time.Duration) {}

	var recA *httptest.ResponseRecorder
	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		recA = issueFailoverPolicyTestRequest(p, "req-a-concurrent")
	}()

	select {
	case <-fallbackStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for req-a to reach request-local fallback")
	}

	recB := issueFailoverPolicyTestRequest(p, "req-b-concurrent")
	if recB.Code != http.StatusOK {
		t.Fatalf("expected concurrent req-b to succeed, got status=%d body=%q", recB.Code, recB.Body.String())
	}
	if got := recB.Header().Get(headerCCNexusEndpoint); got != "Primary" {
		t.Fatalf("expected concurrent req-b to remain on Primary while req-a is on Fallback, got %q", got)
	}

	releaseFallbackOnce.Do(func() { close(releaseFallback) })
	select {
	case <-doneA:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for req-a to finish")
	}
	if recA == nil || recA.Code != http.StatusOK {
		if recA == nil {
			t.Fatal("expected req-a recorder, got nil")
		}
		t.Fatalf("expected req-a to succeed via fallback, got status=%d body=%q", recA.Code, recA.Body.String())
	}
	if got := recA.Header().Get(headerCCNexusEndpoint); got != "Fallback" {
		t.Fatalf("expected req-a to complete on Fallback, got %q", got)
	}

	mu.Lock()
	primaryBHits := primaryHitsByRequest["req-b-concurrent"]
	fallbackBHits := fallbackHitsByRequest["req-b-concurrent"]
	primaryAHits := primaryHitsByRequest["req-a-concurrent"]
	fallbackAHits := fallbackHitsByRequest["req-a-concurrent"]
	mu.Unlock()
	if primaryAHits != endpointSlowFailoverAttempts || fallbackAHits != 1 {
		t.Fatalf("expected req-a to try Primary %d times then Fallback once, got primary=%d fallback=%d", endpointSlowFailoverAttempts, primaryAHits, fallbackAHits)
	}
	if primaryBHits != 1 || fallbackBHits != 0 {
		t.Fatalf("expected req-b to use only Primary once, got primary=%d fallback=%d", primaryBHits, fallbackBHits)
	}
	if got := p.GetCurrentEndpointName(); got != "Primary" {
		t.Fatalf("expected global current endpoint to remain Primary, got %q", got)
	}

	logs := joinedProxyLogs()
	if !strings.Contains(logs, "request_id=req-a-concurrent") || !strings.Contains(logs, "failover_scope=request_local") {
		t.Fatalf("expected req-a request-local failover log, got logs:\n%s", logs)
	}
	if strings.Contains(logs, "[SWITCH]") {
		t.Fatalf("expected no global switch log during concurrent request-local fallback, got logs:\n%s", logs)
	}
}

func TestRequestLocalFallbackStreamingEndpointCanCompleteWhenNotGlobalCurrent(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"用户额度不足, 剩余额度: ＄0.000000","type":"new_api_error","code":"insufficient_user_quota"}}`))
	}))
	defer primary.Close()

	var fallbackHits int
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer fallback.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", primary.URL),
		failoverPolicyTestEndpoint("Fallback", fallback.URL),
	}, primary.Client())

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-stream-local-fallback")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected stream fallback to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if fallbackHits != 1 {
		t.Fatalf("expected fallback stream endpoint to be hit once, got %d", fallbackHits)
	}
	if got := p.GetCurrentEndpointName(); got != "Primary" {
		t.Fatalf("expected global current endpoint to remain Primary, got %q", got)
	}
	if !strings.Contains(rec.Body.String(), "response.completed") {
		t.Fatalf("expected fallback stream body to reach client, got %q", rec.Body.String())
	}
	logs := joinedProxyLogs()
	if strings.Contains(logs, "Endpoint switched during streaming") {
		t.Fatalf("did not expect request-local fallback stream to be terminated as switched, logs:\n%s", logs)
	}
}

func TestClientCanceledRequestDoesNotFailoverOrRecordFailureEvent(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "application/json")
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
	var failureEvents int
	p.SetOnEndpointRuntimeChanged(func(event EndpointRuntimeEvent) {
		if event.Event == "failure" {
			failureEvents++
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-client-canceled")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if fallbackHits != 0 {
		t.Fatalf("expected client cancellation not to fail over, fallback hits=%d", fallbackHits)
	}
	if failureEvents != 0 {
		t.Fatalf("expected client cancellation not to emit failure events, got %d", failureEvents)
	}
	logs := joinedProxyLogs()
	if strings.Contains(logs, "failover_scope=request_local") {
		t.Fatalf("expected no failover after client cancellation, logs:\n%s", logs)
	}
	_ = primaryHits
}

func TestForceStreamAggregationClientCancelStopsWithoutFailover(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	primaryStreaming := make(chan struct{})
	var primaryStreamingOnce sync.Once
	releasePrimary := make(chan struct{})
	var releasePrimaryOnce sync.Once
	t.Cleanup(func() {
		releasePrimaryOnce.Do(func() { close(releasePrimary) })
	})

	var primaryHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		primaryStreamingOnce.Do(func() { close(primaryStreaming) })
		select {
		case <-r.Context().Done():
		case <-releasePrimary:
		case <-time.After(3 * time.Second):
		}
	}))
	defer primary.Close()

	var fallbackHits int
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-fallback","usage":{"input_tokens":1,"output_tokens":2},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer fallback.Close()

	forceStreamPrimary := failoverPolicyTestEndpoint("Primary", primary.URL)
	forceStreamPrimary.ForceStream = true
	p := newFailoverPolicyTestProxy([]config.Endpoint{
		forceStreamPrimary,
		failoverPolicyTestEndpoint("Fallback", fallback.URL),
	}, primary.Client())
	var failureEvents int
	p.SetOnEndpointRuntimeChanged(func(event EndpointRuntimeEvent) {
		if event.Event == "failure" {
			failureEvents++
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-force-stream-client-canceled")
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		p.handleProxy(rec, req)
	}()

	select {
	case <-primaryStreaming:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for primary force-stream response")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for canceled force-stream request")
	}

	if primaryHits != 1 {
		t.Fatalf("expected primary to be hit once, got %d", primaryHits)
	}
	if fallbackHits != 0 {
		t.Fatalf("expected client cancellation during aggregation not to fail over, fallback hits=%d", fallbackHits)
	}
	if failureEvents != 0 {
		t.Fatalf("expected client cancellation during aggregation not to emit failure events, got %d", failureEvents)
	}
	logs := joinedProxyLogs()
	if strings.Contains(logs, "failover_scope=request_local") {
		t.Fatalf("expected no failover after aggregation cancellation, logs:\n%s", logs)
	}
	if strings.Contains(logs, "aggregate_streaming_failed") {
		t.Fatalf("expected aggregation cancellation not to be recorded as aggregate failure, logs:\n%s", logs)
	}
}

func TestClientCanceledRequestDoesNotPersistFailureStatus(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "application/json")
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

	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ccnexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", primary.URL),
		failoverPolicyTestEndpoint("Fallback", fallback.URL),
	})
	p := New(cfg, &noopStatsStorage{}, store, "test-device")
	p.httpClient = primary.Client()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-client-canceled-runtime")
	rec := httptest.NewRecorder()

	p.handleProxy(rec, req)

	if fallbackHits != 0 {
		t.Fatalf("expected client cancellation not to fail over, fallback hits=%d", fallbackHits)
	}
	statuses, err := store.GetEndpointRuntimeStatuses()
	if err != nil {
		t.Fatalf("get runtime statuses: %v", err)
	}
	status := statuses["Primary"]
	if status == nil {
		t.Fatal("expected Primary attempt status to be recorded")
	}
	if status.LastAttemptAt == nil {
		t.Fatal("expected client-canceled request to record an attempt")
	}
	if status.LastFailureAt != nil || status.LastFailureReason != "" {
		t.Fatalf("expected client cancellation not to persist failure status, got %#v", status)
	}
	_ = primaryHits
}

func TestStreamingUpstreamErrorRetriesBeforeRequestLocalFallback(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	var fallbackHits int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "primary.example":
			primaryHits++
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       failingReadCloser{err: errors.New("stream error: stream ID 77; INTERNAL_ERROR; received from peer")},
				Request:    req,
			}, nil
		case "fallback.example":
			fallbackHits++
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(strings.Join([]string{
					`data: {"type":"response.output_text.delta","delta":"ok"}`,
					"",
					`data: {"type":"response.completed","response":{"id":"resp-fallback","object":"response","status":"completed","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`,
					"",
					"data: [DONE]",
					"",
				}, "\n"))),
				Request: req,
			}, nil
		default:
			t.Fatalf("unexpected upstream host %q", req.URL.Host)
			return nil, nil
		}
	})}

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", "https://primary.example"),
		failoverPolicyTestEndpoint("Fallback", "https://fallback.example"),
	}, client)
	var currentEvents []EndpointCurrentEvent
	p.SetOnCurrentEndpointChanged(func(event EndpointCurrentEvent) {
		currentEvents = append(currentEvents, event)
	})

	streamReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	streamReq.Header.Set("Content-Type", "application/json")
	streamReq.Header.Set(headerCCNexusRequestID, "req-stream-error-cooldown")
	streamRec := httptest.NewRecorder()
	p.handleProxy(streamRec, streamReq)

	if streamRec.Code != http.StatusOK {
		t.Fatalf("expected stream fallback to succeed, got status=%d body=%q", streamRec.Code, streamRec.Body.String())
	}
	if primaryHits != endpointFastFailoverAttempts {
		t.Fatalf("expected streaming request to retry Primary %d times before fallback, got %d", endpointFastFailoverAttempts, primaryHits)
	}
	if fallbackHits != 1 {
		t.Fatalf("expected request-local fallback to be used once, got %d", fallbackHits)
	}
	p.cooldownMu.RLock()
	cooldown, cooled := p.endpointCooldowns["Primary"]
	p.cooldownMu.RUnlock()
	if !cooled || cooldown.Reason != "upstream_stream_error" {
		t.Fatalf("expected Primary cooldown for upstream_stream_error, got cooled=%v cooldown=%#v", cooled, cooldown)
	}
	if got := p.GetCurrentEndpointName(); got != "Primary" {
		t.Fatalf("expected global current endpoint to remain Primary, got %q", got)
	}
	if len(currentEvents) != 0 {
		t.Fatalf("expected no current endpoint events, got %#v", currentEvents)
	}
	body := streamRec.Body.String()
	if !strings.Contains(body, "response.output_text.delta") || strings.Contains(body, "event: error") {
		t.Fatalf("expected fallback stream output without final SSE error, got %q", body)
	}

	logs := joinedProxyLogs()
	for _, want := range []string{
		"Streaming response failed before semantic output",
		"retry_reason=upstream_stream_error",
		"[FAILOVER] Primary",
		"failover_reason=upstream_stream_error",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %q, got logs:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "[AUTO SWITCH]") {
		t.Fatalf("expected no auto switch log after request-local stream fallback, got logs:\n%s", logs)
	}
}

func TestStreamingUpstreamErrorAfterSemanticDataWritesErrorAndSwitches(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	var fallbackHits int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "primary.example":
			primaryHits++
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: partialFailingReadCloser{
					reader: strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"),
					err:    context.DeadlineExceeded,
				},
				Request: req,
			}, nil
		case "fallback.example":
			fallbackHits++
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"resp-fallback","usage":{"input_tokens":1,"output_tokens":2},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)),
				Request:    req,
			}, nil
		default:
			t.Fatalf("unexpected upstream host %q", req.URL.Host)
			return nil, nil
		}
	})}

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", "https://primary.example"),
		failoverPolicyTestEndpoint("Fallback", "https://fallback.example"),
	}, client)
	var currentEvents []EndpointCurrentEvent
	p.SetOnCurrentEndpointChanged(func(event EndpointCurrentEvent) {
		currentEvents = append(currentEvents, event)
	})

	streamReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	streamReq.Header.Set("Content-Type", "application/json")
	streamReq.Header.Set(headerCCNexusRequestID, "req-stream-error-after-data")
	streamRec := httptest.NewRecorder()
	p.handleProxy(streamRec, streamReq)

	if primaryHits != 1 {
		t.Fatalf("expected first streaming request to hit Primary once, got %d", primaryHits)
	}
	if fallbackHits != 0 {
		t.Fatalf("expected in-flight streaming error not to replay on fallback, got fallback hits=%d", fallbackHits)
	}
	body := streamRec.Body.String()
	for _, want := range []string{
		"response.output_text.delta",
		"hello",
		"event: error",
		`"type":"upstream_stream_error"`,
		"Upstream stream interrupted",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected downstream body to contain %q, got %q", want, body)
		}
	}
	if got := p.GetCurrentEndpointName(); got != "Primary" {
		t.Fatalf("expected global current endpoint to remain Primary, got %q", got)
	}
	if len(currentEvents) != 0 {
		t.Fatalf("expected no current endpoint events, got %#v", currentEvents)
	}
	p.cooldownMu.RLock()
	_, cooled := p.endpointCooldowns["Primary"]
	p.cooldownMu.RUnlock()
	if cooled {
		t.Fatal("expected post-output stream interruption not to cool Primary")
	}

	logs := joinedProxyLogs()
	for _, want := range []string{
		"Streaming response failed after semantic output",
		"retry_reason=upstream_stream_error",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %q, got logs:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "[AUTO SWITCH]") {
		t.Fatalf("expected no auto switch log after post-output stream interruption, got logs:\n%s", logs)
	}
}

func TestStreamingUpstreamErrorDoesNotSwitchGlobalWhenFailedEndpointIsNotCurrent(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	var fallbackHits int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "primary.example":
			primaryHits++
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"resp-primary","usage":{"input_tokens":1,"output_tokens":2},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)),
				Request:    req,
			}, nil
		case "fallback.example":
			fallbackHits++
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       failingReadCloser{err: errors.New("stream error: stream ID 99; INTERNAL_ERROR; received from peer")},
				Request:    req,
			}, nil
		default:
			t.Fatalf("unexpected upstream host %q", req.URL.Host)
			return nil, nil
		}
	})}

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", "https://primary.example"),
		failoverPolicyTestEndpoint("Fallback", "https://fallback.example"),
	}, client)
	var currentEvents []EndpointCurrentEvent
	p.SetOnCurrentEndpointChanged(func(event EndpointCurrentEvent) {
		currentEvents = append(currentEvents, event)
	})

	streamReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	streamReq.Header.Set("Content-Type", "application/json")
	streamReq.Header.Set("X-CCN-Endpoint", "Fallback")
	streamReq.Header.Set(headerCCNexusRequestID, "req-specified-stream-error-no-global-switch")
	streamRec := httptest.NewRecorder()
	p.handleProxy(streamRec, streamReq)

	if primaryHits != 0 {
		t.Fatalf("expected specified failing request not to hit Primary, got %d", primaryHits)
	}
	if fallbackHits != endpointFastFailoverAttempts {
		t.Fatalf("expected specified failing request to retry Fallback %d times, got %d", endpointFastFailoverAttempts, fallbackHits)
	}
	if got := p.GetCurrentEndpointName(); got != "Primary" {
		t.Fatalf("expected global current endpoint to remain Primary, got %q", got)
	}
	if len(currentEvents) != 0 {
		t.Fatalf("expected no current endpoint events, got %#v", currentEvents)
	}

	logs := joinedProxyLogs()
	if strings.Contains(logs, "[AUTO SWITCH]") {
		t.Fatalf("expected no auto switch log when failed endpoint is not current, got logs:\n%s", logs)
	}
}

func TestTransientNetworkErrorRetriesOnceThenFailsOver(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	var fallbackHits int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "primary.example":
			primaryHits++
			return nil, errors.New("net/http: timeout awaiting response headers")
		case "fallback.example":
			fallbackHits++
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"resp-fallback","usage":{"input_tokens":1,"output_tokens":2},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)),
				Request:    req,
			}, nil
		default:
			t.Fatalf("unexpected upstream host %q", req.URL.Host)
			return nil, nil
		}
	})}

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", "https://primary.example"),
		failoverPolicyTestEndpoint("Fallback", "https://fallback.example"),
	}, client)

	rec := issueFailoverPolicyTestRequest(p, "req-transient-failover")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected transient network failover to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if primaryHits != endpointFastFailoverAttempts {
		t.Fatalf("expected Primary to be tried %d times, got %d", endpointFastFailoverAttempts, primaryHits)
	}
	if fallbackHits != 1 {
		t.Fatalf("expected Fallback to be used once, got %d", fallbackHits)
	}
	if got := rec.Header().Get(headerCCNexusEndpoint); got != "Fallback" {
		t.Fatalf("expected final endpoint Fallback, got %q", got)
	}
	if got := p.GetCurrentEndpointName(); got != "Primary" {
		t.Fatalf("expected global current endpoint to remain Primary, got %q", got)
	}

	logs := joinedProxyLogs()
	for _, want := range []string{
		"cooldown_reason=transient_network_error",
		"failover_scope=request_local",
		"failover_reason=transient_network_error",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %q, got logs:\n%s", want, logs)
		}
	}
}

func TestStreamingResponseHeaderTimeoutKeepsDownstreamOpenAndFallsBack(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	var fallbackHits int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "primary.example":
			primaryHits++
			<-req.Context().Done()
			return nil, req.Context().Err()
		case "fallback.example":
			fallbackHits++
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(strings.Join([]string{
					`data: {"type":"response.output_text.delta","delta":"ok"}`,
					"",
					`data: {"type":"response.completed","response":{"id":"resp-fallback","object":"response","status":"completed","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`,
					"",
					"data: [DONE]",
					"",
				}, "\n"))),
				Request: req,
			}, nil
		default:
			t.Fatalf("unexpected upstream host %q", req.URL.Host)
			return nil, nil
		}
	})}

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", "https://primary.example"),
		failoverPolicyTestEndpoint("Fallback", "https://fallback.example"),
	}, client)
	p.streamHeaderTimeout = 10 * time.Millisecond
	p.streamHeartbeatInterval = time.Hour

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-stream-header-timeout")
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected downstream SSE to remain open and succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if primaryHits != endpointFastFailoverAttempts {
		t.Fatalf("expected Primary to time out %d times before fallback, got %d", endpointFastFailoverAttempts, primaryHits)
	}
	if fallbackHits != 1 {
		t.Fatalf("expected fallback to be used once, got %d", fallbackHits)
	}
	body := rec.Body.String()
	if !strings.Contains(body, ": ccnexus waiting for upstream") || !strings.Contains(body, "response.output_text.delta") {
		t.Fatalf("expected heartbeat plus fallback stream output, got %q", body)
	}
	if strings.Contains(body, "event: error") {
		t.Fatalf("did not expect final SSE error after fallback success, got %q", body)
	}

	logs := joinedProxyLogs()
	if !strings.Contains(logs, "retry_reason=transient_network_error") ||
		!strings.Contains(logs, "failover_reason=transient_network_error") {
		t.Fatalf("expected transient timeout retry/fallback logs, got logs:\n%s", logs)
	}
}

func TestStreamingResponseHeaderTimeoutDisabledByDefault(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	var fallbackHits int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "primary.example":
			primaryHits++
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(20 * time.Millisecond):
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body: io.NopCloser(strings.NewReader(strings.Join([]string{
						`data: {"type":"response.output_text.delta","delta":"ok"}`,
						"",
						`data: {"type":"response.completed","response":{"id":"resp-primary","object":"response","status":"completed","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`,
						"",
						"data: [DONE]",
						"",
					}, "\n"))),
					Request: req,
				}, nil
			}
		case "fallback.example":
			fallbackHits++
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
				Request:    req,
			}, nil
		default:
			t.Fatalf("unexpected upstream host %q", req.URL.Host)
			return nil, nil
		}
	})}

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", "https://primary.example"),
		failoverPolicyTestEndpoint("Fallback", "https://fallback.example"),
	}, client)
	p.streamHeartbeatInterval = time.Hour
	if timeout := p.streamHeaderTimeoutOrDefault(); timeout != 0 {
		t.Fatalf("expected default streaming response-header timeout to be disabled, got %s", timeout)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-stream-header-timeout-default-off")
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected primary stream to succeed, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if primaryHits != 1 {
		t.Fatalf("expected primary to be used once, got %d", primaryHits)
	}
	if fallbackHits != 0 {
		t.Fatalf("expected fallback not to be used when default header timeout is disabled, got %d", fallbackHits)
	}
	if strings.Contains(joinedProxyLogs(), "retry_reason=transient_network_error") {
		t.Fatalf("did not expect transient retry logs when default header timeout is disabled, logs:\n%s", joinedProxyLogs())
	}
}

func TestTransportProtocolErrorDoesNotPenalizeTokenPoolCredential(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("net/http: HTTP/1.x transport connection broken: malformed HTTP response \"\\x00\\x00\\x12\\x04\"")
	})}

	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ccnexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	cred := storage.EndpointCredential{EndpointName: "Primary", ProviderType: "openai", AccessToken: "token-a", Enabled: true}
	if err := store.SaveEndpointCredential(&cred); err != nil {
		t.Fatalf("save credential: %v", err)
	}

	cfg := config.DefaultConfig()
	endpoint := failoverPolicyTestEndpoint("Primary", "https://primary.example")
	endpoint.AuthMode = config.AuthModeTokenPool
	endpoint.APIKey = ""
	cfg.UpdateEndpoints([]config.Endpoint{endpoint})
	p := New(cfg, &noopStatsStorage{}, store, "test-device")
	p.httpClient = client
	p.retrySleep = func(time.Duration) {}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, "req-protocol-error-token")
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected request to fail after retryable protocol errors, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	updated, err := store.GetCredentialByID(cred.ID)
	if err != nil {
		t.Fatalf("load credential: %v", err)
	}
	if updated == nil {
		t.Fatal("expected credential to exist")
	}
	if updated.FailureCount != 0 || strings.TrimSpace(updated.LastError) != "" || updated.Status != "active" {
		t.Fatalf("expected transport protocol error not to penalize credential, got %#v", updated)
	}
	if logs := joinedProxyLogs(); !strings.Contains(logs, "retry_reason=transport_protocol_error") {
		t.Fatalf("expected transport protocol retry logs, got logs:\n%s", logs)
	}
}

func TestTransientNetworkErrorSingleRetryCanRecoverOnSameEndpoint(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	var primaryHits int
	var fallbackHits int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "primary.example":
			primaryHits++
			if primaryHits == 1 {
				return nil, errors.New("net/http: timeout awaiting response headers")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"resp-primary","usage":{"input_tokens":1,"output_tokens":2},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)),
				Request:    req,
			}, nil
		case "fallback.example":
			fallbackHits++
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"resp-fallback","usage":{"input_tokens":1,"output_tokens":2},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)),
				Request:    req,
			}, nil
		default:
			t.Fatalf("unexpected upstream host %q", req.URL.Host)
			return nil, nil
		}
	})}

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", "https://primary.example"),
		failoverPolicyTestEndpoint("Fallback", "https://fallback.example"),
	}, client)

	rec := issueFailoverPolicyTestRequest(p, "req-transient-recover")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected transient retry to recover on Primary, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if primaryHits != 2 {
		t.Fatalf("expected Primary to be tried twice, got %d", primaryHits)
	}
	if fallbackHits != 0 {
		t.Fatalf("expected Fallback not to be used, got %d", fallbackHits)
	}
	if got := rec.Header().Get(headerCCNexusEndpoint); got != "Primary" {
		t.Fatalf("expected final endpoint Primary, got %q", got)
	}
	if logs := joinedProxyLogs(); strings.Contains(logs, "failover_reason=transient_network_error") {
		t.Fatalf("expected no transient failover after one recovered timeout, got logs:\n%s", logs)
	}
}

func TestRateLimitedRetryUsesBackoffBeforeSameEndpointRetry(t *testing.T) {
	logger.GetLogger().Clear()
	logger.GetLogger().SetMinLevel(logger.DEBUG)

	primaryHits := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.Header().Set("Content-Type", "application/json")
		if primaryHits == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"Too Many Requests"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"resp-primary","usage":{"input_tokens":1,"output_tokens":2},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer primary.Close()

	p := newFailoverPolicyTestProxy([]config.Endpoint{
		failoverPolicyTestEndpoint("Primary", primary.URL),
	}, primary.Client())
	var sleeps []time.Duration
	p.retrySleep = func(d time.Duration) {
		sleeps = append(sleeps, d)
	}

	rec := issueFailoverPolicyTestRequest(p, "req-backoff")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected request to succeed after backoff retry, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if primaryHits != 2 {
		t.Fatalf("expected one rate-limited attempt and one success attempt, got %d hits", primaryHits)
	}
	if len(sleeps) != 1 {
		t.Fatalf("expected exactly one backoff sleep, got %d", len(sleeps))
	}
	if sleeps[0] != 800*time.Millisecond {
		t.Fatalf("expected first rate-limit backoff to be 800ms, got %s", sleeps[0])
	}
}

func issueFailoverPolicyTestRequest(p *Proxy, requestID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":false,"input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerCCNexusRequestID, requestID)
	rec := httptest.NewRecorder()
	p.handleProxy(rec, req)
	return rec
}

func joinedProxyLogs() string {
	logs := logger.GetLogger().GetLogs()
	var builder strings.Builder
	for _, entry := range logs {
		builder.WriteString(entry.Message)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func disableEndpointCooldownsForTest(p *Proxy) {
	p.config.UpdateFailover(&config.FailoverConfig{
		RecoveredEndpointPolicy: config.RecoveredEndpointPolicyDeprioritize,
		Cooldowns:               &config.FailoverCooldownConfig{},
		CircuitBreaker:          &config.FailoverCircuitBreakerConfig{},
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type failingReadCloser struct {
	err error
}

func (r failingReadCloser) Read([]byte) (int, error) {
	return 0, r.err
}

func (r failingReadCloser) Close() error {
	return nil
}

type partialFailingReadCloser struct {
	reader *strings.Reader
	err    error
}

func (r partialFailingReadCloser) Read(p []byte) (int, error) {
	if r.reader != nil && r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	return 0, r.err
}

func (r partialFailingReadCloser) Close() error {
	return nil
}
