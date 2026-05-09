package proxy

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
)

func TestUpdateConfigPreservesCurrentEndpointByNameAfterReorder(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		failoverPolicyTestEndpoint("A", "https://a.example"),
		failoverPolicyTestEndpoint("B", "https://b.example"),
		failoverPolicyTestEndpoint("C", "https://c.example"),
	})
	p := New(cfg, &noopStatsStorage{}, nil, "test-device")

	if err := p.SetCurrentEndpoint("B"); err != nil {
		t.Fatalf("set current endpoint: %v", err)
	}
	currentName := p.GetCurrentEndpointName()

	cfg.UpdateEndpoints([]config.Endpoint{
		failoverPolicyTestEndpoint("B", "https://b.example"),
		failoverPolicyTestEndpoint("C", "https://c.example"),
		failoverPolicyTestEndpoint("A", "https://a.example"),
	})
	if err := p.UpdateConfigPreservingCurrentName(cfg, currentName); err != nil {
		t.Fatalf("update config: %v", err)
	}

	if got := p.GetCurrentEndpointName(); got != "B" {
		t.Fatalf("expected current endpoint B after reorder, got %q", got)
	}
}

func TestSetCurrentEndpointDoesNotCancelInFlightRequests(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		failoverPolicyTestEndpoint("A", "https://a.example"),
		failoverPolicyTestEndpoint("B", "https://b.example"),
	})
	p := New(cfg, &noopStatsStorage{}, nil, "test-device")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.endpointCtx["A"] = ctx
	p.endpointCancel["A"] = cancel

	if err := p.SetCurrentEndpoint("B"); err != nil {
		t.Fatalf("set current endpoint: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("expected manual default switch not to cancel in-flight endpoint context, got %v", ctx.Err())
	}
}

func TestConfigReorderPreservesEndpointCooldown(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		failoverPolicyTestEndpoint("A", "https://a.example"),
		failoverPolicyTestEndpoint("B", "https://b.example"),
	})
	p := New(cfg, &noopStatsStorage{}, nil, "test-device")

	p.markEndpointCooldown("A", "quota_exhausted", 10*time.Minute, requestObservability{RequestID: "req-cooldown"}, 1)
	currentName := p.GetCurrentEndpointName()
	cfg.UpdateEndpoints([]config.Endpoint{
		failoverPolicyTestEndpoint("B", "https://b.example"),
		failoverPolicyTestEndpoint("A", "https://a.example"),
	})
	if err := p.UpdateConfigPreservingCurrentName(cfg, currentName); err != nil {
		t.Fatalf("update config: %v", err)
	}

	p.cooldownMu.RLock()
	_, stillCooled := p.endpointCooldowns["A"]
	p.cooldownMu.RUnlock()
	if !stillCooled {
		t.Fatal("expected reorder-only config update to preserve endpoint cooldown")
	}
}

func TestConfigIdentityChangeClearsOnlyChangedEndpointCooldown(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		failoverPolicyTestEndpoint("A", "https://a.example"),
		failoverPolicyTestEndpoint("B", "https://b.example"),
	})
	p := New(cfg, &noopStatsStorage{}, nil, "test-device")

	p.markEndpointCooldown("A", "quota_exhausted", 10*time.Minute, requestObservability{RequestID: "req-cooldown-a"}, 1)
	p.markEndpointCooldown("B", "quota_exhausted", 10*time.Minute, requestObservability{RequestID: "req-cooldown-b"}, 1)
	currentName := p.GetCurrentEndpointName()

	changedA := failoverPolicyTestEndpoint("A", "https://a-new.example")
	cfg.UpdateEndpoints([]config.Endpoint{
		changedA,
		failoverPolicyTestEndpoint("B", "https://b.example"),
	})
	if err := p.UpdateConfigPreservingCurrentName(cfg, currentName); err != nil {
		t.Fatalf("update config: %v", err)
	}

	p.cooldownMu.RLock()
	_, aCooled := p.endpointCooldowns["A"]
	_, bCooled := p.endpointCooldowns["B"]
	p.cooldownMu.RUnlock()
	if aCooled {
		t.Fatal("expected identity-changing endpoint update to clear A cooldown")
	}
	if !bCooled {
		t.Fatal("expected unchanged endpoint B cooldown to remain")
	}
}

func TestRequestPlanStartsAtCurrentEndpointAfterFilteringEarlierCooldown(t *testing.T) {
	endpoints := []config.Endpoint{
		failoverPolicyTestEndpoint("A", "https://a.example"),
		failoverPolicyTestEndpoint("B", "https://b.example"),
		failoverPolicyTestEndpoint("C", "https://c.example"),
	}
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints(endpoints)
	p := New(cfg, &noopStatsStorage{}, nil, "test-device")
	if err := p.SetCurrentEndpoint("B"); err != nil {
		t.Fatalf("set current endpoint: %v", err)
	}
	p.markEndpointCooldown("A", "quota_exhausted", 10*time.Minute, requestObservability{RequestID: "req-plan"}, 1)

	available := p.getRequestPlanEndpoints(endpoints, requestObservability{RequestID: "req-plan"})
	plan := newRequestEndpointPlanForCurrent(available, endpoints, p.GetCurrentEndpointName())
	if got := plan.Current().Name; got != "B" {
		t.Fatalf("expected request plan to start at current endpoint B after filtering A, got %q", got)
	}
}

func TestRequestPlanStartsAtNextEndpointWhenCurrentIsCooled(t *testing.T) {
	endpoints := []config.Endpoint{
		failoverPolicyTestEndpoint("A", "https://a.example"),
		failoverPolicyTestEndpoint("B", "https://b.example"),
		failoverPolicyTestEndpoint("C", "https://c.example"),
	}
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints(endpoints)
	p := New(cfg, &noopStatsStorage{}, nil, "test-device")
	p.markEndpointCooldown("A", "quota_exhausted", 10*time.Minute, requestObservability{RequestID: "req-plan-current-cooled"}, 1)

	available := p.getRequestPlanEndpoints(endpoints, requestObservability{RequestID: "req-plan-current-cooled"})
	plan := newRequestEndpointPlanForCurrent(available, endpoints, p.GetCurrentEndpointName())
	if got := plan.Current().Name; got != "B" {
		t.Fatalf("expected request plan to start at next available endpoint B when current A is cooled, got %q", got)
	}
}

func TestRequestPlanDeprioritizesRecoveredCurrentEndpoint(t *testing.T) {
	endpoints := []config.Endpoint{
		failoverPolicyTestEndpoint("A", "https://a.example"),
		failoverPolicyTestEndpoint("B", "https://b.example"),
		failoverPolicyTestEndpoint("C", "https://c.example"),
	}
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints(endpoints)
	p := New(cfg, &noopStatsStorage{}, nil, "test-device")

	p.cooldownMu.Lock()
	p.endpointCooldowns["A"] = endpointCooldown{Reason: "quota_exhausted", Until: time.Now().Add(-time.Second)}
	p.cooldownMu.Unlock()

	available := p.getRequestPlanEndpoints(endpoints, requestObservability{RequestID: "req-plan-recovered"})
	plan := newRequestEndpointPlanForCurrentWithSkip(available, endpoints, p.GetCurrentEndpointName(), p.isEndpointDeprioritized(p.GetCurrentEndpointName()))
	if got := plan.Current().Name; got != "B" {
		t.Fatalf("expected recovered current endpoint to be deprioritized behind B, got %q", got)
	}
	if got := plan.Advance().Name; got != "C" {
		t.Fatalf("expected C after B, got %q", got)
	}
	if got := plan.Advance().Name; got != "A" {
		t.Fatalf("expected recovered A to remain as final fallback, got %q", got)
	}
}

func TestRequestPlanAutoReturnsRecoveredCurrentEndpoint(t *testing.T) {
	endpoints := []config.Endpoint{
		failoverPolicyTestEndpoint("A", "https://a.example"),
		failoverPolicyTestEndpoint("B", "https://b.example"),
	}
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints(endpoints)
	cfg.UpdateFailover(&config.FailoverConfig{RecoveredEndpointPolicy: config.RecoveredEndpointPolicyAutoReturn})
	p := New(cfg, &noopStatsStorage{}, nil, "test-device")

	p.cooldownMu.Lock()
	p.endpointCooldowns["A"] = endpointCooldown{Reason: "quota_exhausted", Until: time.Now().Add(-time.Second)}
	p.cooldownMu.Unlock()

	available := p.getRequestPlanEndpoints(endpoints, requestObservability{RequestID: "req-plan-auto-return"})
	plan := newRequestEndpointPlanForCurrentWithSkip(available, endpoints, p.GetCurrentEndpointName(), p.isEndpointDeprioritized(p.GetCurrentEndpointName()))
	if got := plan.Current().Name; got != "A" {
		t.Fatalf("expected auto_return policy to start on recovered current A, got %q", got)
	}
	p.cooldownMu.RLock()
	_, stillCooled := p.endpointCooldowns["A"]
	p.cooldownMu.RUnlock()
	if stillCooled {
		t.Fatal("expected auto_return policy to clear expired cooldown")
	}
}

func TestManualSwitchClearsEndpointCooldown(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		failoverPolicyTestEndpoint("A", "https://a.example"),
		failoverPolicyTestEndpoint("B", "https://b.example"),
	})
	p := New(cfg, &noopStatsStorage{}, nil, "test-device")
	p.markEndpointCooldown("B", "quota_exhausted", time.Hour, requestObservability{RequestID: "req-manual-clear"}, 1)

	if err := p.SetCurrentEndpoint("B"); err != nil {
		t.Fatalf("set current endpoint: %v", err)
	}
	p.cooldownMu.RLock()
	_, stillCooled := p.endpointCooldowns["B"]
	p.cooldownMu.RUnlock()
	if stillCooled {
		t.Fatal("expected manual switch to clear endpoint cooldown")
	}
}

func TestCooldownDurationForFailureReasons(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateFailover(&config.FailoverConfig{
		RecoveredEndpointPolicy: config.RecoveredEndpointPolicyDeprioritize,
		Cooldowns: &config.FailoverCooldownConfig{
			QuotaExhaustedSec:   11,
			RateLimitedSec:      12,
			UpstreamErrorSec:    13,
			NetworkErrorSec:     14,
			TokenUnavailableSec: 15,
			ConfigErrorSec:      16,
		},
	})
	p := New(cfg, &noopStatsStorage{}, nil, "test-device")

	cases := map[string]time.Duration{
		"quota_exhausted":            11 * time.Second,
		"rate_limited":               12 * time.Second,
		"upstream_5xx":               13 * time.Second,
		"retryable_status":           13 * time.Second,
		"upstream_stream_error":      13 * time.Second,
		"streaming_failed":           13 * time.Second,
		"send_request_failed":        14 * time.Second,
		"transient_network_error":    14 * time.Second,
		"no_usable_token":            15 * time.Second,
		"credential_select_failed":   15 * time.Second,
		"empty_api_key":              16 * time.Second,
		"prepare_transformer_failed": 16 * time.Second,
		"build_request_failed":       16 * time.Second,
		"non_stream_response_failed": 0,
	}
	for reason, want := range cases {
		if got := p.cooldownDurationForReason(reason, nil); got != want {
			t.Fatalf("reason %s: expected %s, got %s", reason, want, got)
		}
	}

	headers := http.Header{}
	headers.Set("Retry-After", "42")
	if got := p.cooldownDurationForReason("rate_limited", headers); got != 42*time.Second {
		t.Fatalf("expected Retry-After cooldown 42s, got %s", got)
	}
}

func TestUpdateConfigFallsBackAndEmitsEventWhenCurrentEndpointDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		failoverPolicyTestEndpoint("A", "https://a.example"),
		failoverPolicyTestEndpoint("B", "https://b.example"),
		failoverPolicyTestEndpoint("C", "https://c.example"),
	})
	p := New(cfg, &noopStatsStorage{}, nil, "test-device")
	if err := p.SetCurrentEndpoint("B"); err != nil {
		t.Fatalf("set current endpoint: %v", err)
	}

	var events []EndpointCurrentEvent
	p.SetOnCurrentEndpointChanged(func(event EndpointCurrentEvent) {
		events = append(events, event)
	})

	currentName := p.GetCurrentEndpointName()
	disabledB := failoverPolicyTestEndpoint("B", "https://b.example")
	disabledB.Enabled = false
	cfg.UpdateEndpoints([]config.Endpoint{
		failoverPolicyTestEndpoint("A", "https://a.example"),
		disabledB,
		failoverPolicyTestEndpoint("C", "https://c.example"),
	})
	if err := p.UpdateConfigPreservingCurrentName(cfg, currentName); err != nil {
		t.Fatalf("update config: %v", err)
	}

	if got := p.GetCurrentEndpointName(); got != "A" {
		t.Fatalf("expected fallback current endpoint A, got %q", got)
	}
	if len(events) != 1 {
		t.Fatalf("expected one current endpoint event, got %d", len(events))
	}
	if events[0].PreviousName != "B" || events[0].Name != "A" || events[0].Reason != "config_update" {
		t.Fatalf("unexpected current endpoint event: %#v", events[0])
	}
}
