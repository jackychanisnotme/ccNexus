package proxy

import (
	"net/http"
	"strings"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
)

type endpointCooldown struct {
	Reason string
	Until  time.Time
}

func (p *Proxy) markEndpointCooldown(endpointName string, reason string, duration time.Duration, obs requestObservability, attemptNumber int) {
	if endpointName == "" || duration <= 0 {
		return
	}
	until := time.Now().Add(duration)

	p.cooldownMu.Lock()
	if p.endpointCooldowns == nil {
		p.endpointCooldowns = make(map[string]endpointCooldown)
	}
	p.endpointCooldowns[endpointName] = endpointCooldown{Reason: sanitizeLogField(reason), Until: until}
	p.cooldownMu.Unlock()

	logger.Debug("[COOLDOWN] %s cooldown=%s until=%s %s cooldown_reason=%s",
		endpointName,
		duration.Round(time.Millisecond),
		until.Format(time.RFC3339),
		requestLogFields(obs, endpointName, attemptNumber, 0, reason),
		sanitizeLogField(reason),
	)
}

func (p *Proxy) clearEndpointCooldown(endpointName string) {
	if endpointName == "" {
		return
	}
	p.cooldownMu.Lock()
	defer p.cooldownMu.Unlock()
	delete(p.endpointCooldowns, endpointName)
}

func (p *Proxy) clearEndpointCooldowns() {
	p.cooldownMu.Lock()
	defer p.cooldownMu.Unlock()
	p.endpointCooldowns = make(map[string]endpointCooldown)
}

func (p *Proxy) clearEndpointCooldownsForConfigChange(oldEndpoints []config.Endpoint, newEndpoints []config.Endpoint) {
	if len(oldEndpoints) == 0 {
		return
	}

	oldByName := make(map[string]config.Endpoint, len(oldEndpoints))
	for _, endpoint := range oldEndpoints {
		oldByName[endpoint.Name] = endpoint
	}

	newByName := make(map[string]config.Endpoint, len(newEndpoints))
	for _, endpoint := range newEndpoints {
		newByName[endpoint.Name] = endpoint
	}

	toClear := make([]string, 0)
	for oldName := range oldByName {
		if _, ok := newByName[oldName]; !ok {
			toClear = append(toClear, oldName)
		}
	}
	for newName, newEndpoint := range newByName {
		oldEndpoint, ok := oldByName[newName]
		if !ok {
			continue
		}
		if endpointCooldownIdentityChanged(oldEndpoint, newEndpoint) {
			toClear = append(toClear, newName)
		}
	}
	if len(toClear) == 0 {
		return
	}

	p.cooldownMu.Lock()
	for _, name := range toClear {
		delete(p.endpointCooldowns, name)
	}
	p.cooldownMu.Unlock()

	logger.Debug("[CONFIG UPDATE] Cleared endpoint cooldowns for changed endpoints: %v", toClear)
}

func endpointCooldownIdentityChanged(oldEndpoint config.Endpoint, newEndpoint config.Endpoint) bool {
	return strings.TrimSpace(oldEndpoint.APIUrl) != strings.TrimSpace(newEndpoint.APIUrl) ||
		strings.TrimSpace(oldEndpoint.APIKey) != strings.TrimSpace(newEndpoint.APIKey) ||
		config.NormalizeAuthMode(oldEndpoint.AuthMode) != config.NormalizeAuthMode(newEndpoint.AuthMode) ||
		strings.TrimSpace(strings.ToLower(oldEndpoint.Transformer)) != strings.TrimSpace(strings.ToLower(newEndpoint.Transformer)) ||
		strings.TrimSpace(oldEndpoint.Model) != strings.TrimSpace(newEndpoint.Model) ||
		strings.TrimSpace(oldEndpoint.Thinking) != strings.TrimSpace(newEndpoint.Thinking) ||
		oldEndpoint.ForceStream != newEndpoint.ForceStream
}

func (p *Proxy) getRequestPlanEndpoints(endpoints []config.Endpoint, obs requestObservability) []config.Endpoint {
	if len(endpoints) <= 1 {
		return endpoints
	}

	now := time.Now()
	available := make([]config.Endpoint, 0, len(endpoints))
	deprioritized := make([]config.Endpoint, 0)
	policy := p.recoveredEndpointPolicy()

	p.cooldownMu.Lock()
	defer p.cooldownMu.Unlock()

	for _, endpoint := range endpoints {
		cooldown, ok := p.endpointCooldowns[endpoint.Name]
		if !ok {
			available = append(available, endpoint)
			continue
		}
		if !cooldown.Until.After(now) {
			if policy == config.RecoveredEndpointPolicyAutoReturn {
				delete(p.endpointCooldowns, endpoint.Name)
				available = append(available, endpoint)
			} else {
				deprioritized = append(deprioritized, endpoint)
				logger.Debug("[COOLDOWN] Recovered endpoint deprioritized for request plan: %s %s cooldown_reason=%s",
					endpoint.Name,
					requestLogFields(obs, endpoint.Name, 0, 0, cooldown.Reason),
					sanitizeLogField(cooldown.Reason),
				)
			}
			continue
		}
		logger.Debug("[COOLDOWN] Skipping cooled endpoint for request plan: %s remaining=%s %s cooldown_reason=%s",
			endpoint.Name,
			cooldown.Until.Sub(now).Round(time.Millisecond),
			requestLogFields(obs, endpoint.Name, 0, 0, cooldown.Reason),
			sanitizeLogField(cooldown.Reason),
		)
	}

	if len(available) == 0 && len(deprioritized) == 0 {
		logger.Debug("[COOLDOWN] All enabled endpoints are cooled; using full endpoint list %s", requestLogFields(obs, "", 0, 0, "all_endpoints_cooled"))
		return endpoints
	}
	available = append(available, deprioritized...)
	return available
}

func (p *Proxy) recoveredEndpointPolicy() string {
	if p == nil || p.config == nil {
		return config.RecoveredEndpointPolicyDeprioritize
	}
	return p.config.GetFailover().RecoveredEndpointPolicy
}

func (p *Proxy) isEndpointDeprioritized(endpointName string) bool {
	if strings.TrimSpace(endpointName) == "" ||
		p.recoveredEndpointPolicy() != config.RecoveredEndpointPolicyDeprioritize {
		return false
	}

	p.cooldownMu.Lock()
	defer p.cooldownMu.Unlock()
	cooldown, ok := p.endpointCooldowns[endpointName]
	return ok && !cooldown.Until.After(time.Now())
}

func (p *Proxy) markEndpointCooldownForReason(endpointName string, reason string, headers http.Header, obs requestObservability, attemptNumber int) {
	duration := p.cooldownDurationForReason(reason, headers)
	if duration <= 0 {
		return
	}
	p.markEndpointCooldown(endpointName, reason, duration, obs, attemptNumber)
}

func (p *Proxy) cooldownDurationForReason(reason string, headers http.Header) time.Duration {
	failover := config.DefaultFailoverConfig()
	if p != nil && p.config != nil {
		failover = p.config.GetFailover()
	}
	cooldowns := failover.Cooldowns
	if cooldowns == nil {
		cooldowns = config.DefaultFailoverConfig().Cooldowns
	}

	switch sanitizeLogField(reason) {
	case "quota_exhausted":
		return secondsToDuration(cooldowns.QuotaExhaustedSec)
	case "rate_limited":
		if retryAfter := parseRetryAfterHeader(headers.Get("Retry-After")); retryAfter > 0 {
			return retryAfter
		}
		return secondsToDuration(cooldowns.RateLimitedSec)
	case "upstream_5xx", "retryable_status", "upstream_stream_error", "streaming_failed", retryReasonSemanticEmptyResponse:
		return secondsToDuration(cooldowns.UpstreamErrorSec)
	case "send_request_failed", "transient_network_error":
		return secondsToDuration(cooldowns.NetworkErrorSec)
	case "credential_select_failed", "no_usable_token", "credential_refresh_failed":
		return secondsToDuration(cooldowns.TokenUnavailableSec)
	case "empty_api_key", "prepare_transformer_failed", "build_request_failed":
		return secondsToDuration(cooldowns.ConfigErrorSec)
	default:
		return 0
	}
}

func secondsToDuration(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
