package proxy

import (
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
)

const (
	retryReasonCircuitBreakerConsecutive = "circuit_breaker_consecutive_failures"
	retryReasonCircuitBreakerFailureRate = "circuit_breaker_failure_rate"
)

type endpointCircuitBreakerEvent struct {
	At      time.Time
	Failure bool
}

type endpointCircuitBreakerState struct {
	ConsecutiveFailures int
	Events              []endpointCircuitBreakerEvent
}

func (p *Proxy) circuitBreakerConfig() *config.FailoverCircuitBreakerConfig {
	failover := config.DefaultFailoverConfig()
	if p != nil && p.config != nil {
		failover = p.config.GetFailover()
	}
	if failover == nil || failover.CircuitBreaker == nil {
		return config.DefaultFailoverConfig().CircuitBreaker
	}
	return failover.CircuitBreaker
}

func (p *Proxy) recordEndpointCircuitSuccess(endpointName string) {
	if endpointName == "" {
		return
	}

	cfg := p.circuitBreakerConfig()
	now := time.Now()

	p.circuitBreakerMu.Lock()
	defer p.circuitBreakerMu.Unlock()

	if p.endpointCircuitBreakers == nil {
		p.endpointCircuitBreakers = make(map[string]*endpointCircuitBreakerState)
	}
	state := p.endpointCircuitBreakers[endpointName]
	if state == nil {
		state = &endpointCircuitBreakerState{}
		p.endpointCircuitBreakers[endpointName] = state
	}
	state.ConsecutiveFailures = 0
	if cfg.WindowSec > 0 {
		state.Events = append(state.Events, endpointCircuitBreakerEvent{At: now, Failure: false})
		state.Events = pruneEndpointCircuitEvents(state.Events, now.Add(-time.Duration(cfg.WindowSec)*time.Second))
	}
}

func (p *Proxy) recordEndpointCircuitFailure(endpointName, reason string, statusCode int) bool {
	if endpointName == "" {
		return false
	}

	cfg := p.circuitBreakerConfig()
	if cfg.CooldownSec <= 0 {
		return false
	}

	now := time.Now()
	cooldown := time.Duration(cfg.CooldownSec) * time.Second
	triggerReason := ""
	total := 0
	failures := 0
	failureRate := 0.0
	consecutiveFailures := 0

	p.circuitBreakerMu.Lock()
	if p.endpointCircuitBreakers == nil {
		p.endpointCircuitBreakers = make(map[string]*endpointCircuitBreakerState)
	}
	state := p.endpointCircuitBreakers[endpointName]
	if state == nil {
		state = &endpointCircuitBreakerState{}
		p.endpointCircuitBreakers[endpointName] = state
	}

	state.ConsecutiveFailures++
	state.Events = append(state.Events, endpointCircuitBreakerEvent{At: now, Failure: true})
	if cfg.WindowSec > 0 {
		state.Events = pruneEndpointCircuitEvents(state.Events, now.Add(-time.Duration(cfg.WindowSec)*time.Second))
		total, failures = endpointCircuitWindowCounts(state.Events)
	}

	consecutiveFailures = state.ConsecutiveFailures
	if cfg.ConsecutiveFailures > 0 && state.ConsecutiveFailures >= cfg.ConsecutiveFailures {
		triggerReason = retryReasonCircuitBreakerConsecutive
	}

	if triggerReason == "" &&
		cfg.WindowSec > 0 &&
		cfg.MinRequests > 0 &&
		cfg.FailureRateThreshold > 0 {
		if total >= cfg.MinRequests {
			failureRate = float64(failures) / float64(total)
			if failureRate >= cfg.FailureRateThreshold {
				triggerReason = retryReasonCircuitBreakerFailureRate
			}
		}
	}

	if triggerReason != "" {
		state.ConsecutiveFailures = 0
		state.Events = nil
	}
	p.circuitBreakerMu.Unlock()

	if triggerReason == "" {
		return false
	}

	logger.Warn(
		"[CIRCUIT_BREAKER] endpoint=%s cooldown=%s trigger=%s failure_reason=%s status=%d consecutive_failures=%d window_total=%d window_failures=%d failure_rate=%.2f threshold=%.2f",
		endpointName,
		cooldown.Round(time.Millisecond),
		triggerReason,
		sanitizeLogField(reason),
		statusCode,
		consecutiveFailures,
		total,
		failures,
		failureRate,
		cfg.FailureRateThreshold,
	)
	p.markEndpointCooldown(endpointName, triggerReason, cooldown, requestObservability{
		RequestID: "circuit_breaker",
		ClientIP:  "internal",
		Agent:     "AINexus",
	}, 0)
	return true
}

func pruneEndpointCircuitEvents(events []endpointCircuitBreakerEvent, cutoff time.Time) []endpointCircuitBreakerEvent {
	first := 0
	for first < len(events) && events[first].At.Before(cutoff) {
		first++
	}
	if first == 0 {
		return events
	}
	pruned := make([]endpointCircuitBreakerEvent, len(events)-first)
	copy(pruned, events[first:])
	return pruned
}

func endpointCircuitWindowCounts(events []endpointCircuitBreakerEvent) (int, int) {
	failures := 0
	for _, event := range events {
		if event.Failure {
			failures++
		}
	}
	return len(events), failures
}
