package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/storage"
)

type EndpointCurrentEvent struct {
	Name         string `json:"name"`
	PreviousName string `json:"previousName,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type EndpointRuntimeEvent struct {
	EndpointName          string     `json:"endpointName"`
	ActiveCount           int        `json:"activeCount"`
	LastSuccessAt         *time.Time `json:"lastSuccessAt,omitempty"`
	LastFailureAt         *time.Time `json:"lastFailureAt,omitempty"`
	LastFailureReason     string     `json:"lastFailureReason,omitempty"`
	LastFailureStatusCode int        `json:"lastFailureStatusCode,omitempty"`
	LastAttemptAt         *time.Time `json:"lastAttemptAt,omitempty"`
	Event                 string     `json:"event"`
}

func (p *Proxy) getActiveRequestCount(endpointName string) int {
	p.activeRequestsMu.RLock()
	defer p.activeRequestsMu.RUnlock()
	return p.activeRequests[endpointName]
}

func (p *Proxy) emitEndpointRuntimeEvent(endpointName, event string, status *storage.EndpointRuntimeStatus) {
	if p.onEndpointRuntimeChanged == nil || strings.TrimSpace(endpointName) == "" {
		return
	}

	runtimeEvent := EndpointRuntimeEvent{
		EndpointName: endpointName,
		ActiveCount:  p.getActiveRequestCount(endpointName),
		Event:        event,
	}
	if status != nil {
		runtimeEvent.LastSuccessAt = status.LastSuccessAt
		runtimeEvent.LastFailureAt = status.LastFailureAt
		runtimeEvent.LastFailureReason = status.LastFailureReason
		runtimeEvent.LastFailureStatusCode = status.LastFailureStatusCode
		runtimeEvent.LastAttemptAt = status.LastAttemptAt
	}
	p.onEndpointRuntimeChanged(runtimeEvent)
}

func (p *Proxy) upsertEndpointRuntimeStatus(endpointName string, patch storage.EndpointRuntimeStatusPatch) *storage.EndpointRuntimeStatus {
	if p.storage == nil || strings.TrimSpace(endpointName) == "" {
		return nil
	}
	status, err := p.storage.UpsertEndpointRuntimeStatus(endpointName, patch)
	if err != nil {
		logger.Warn("Failed to update endpoint runtime status endpoint=%s: %v", endpointName, err)
		return nil
	}
	return status
}

func (p *Proxy) recordEndpointAttempt(endpointName string) *storage.EndpointRuntimeStatus {
	now := time.Now().UTC()
	status := p.upsertEndpointRuntimeStatus(endpointName, storage.EndpointRuntimeStatusPatch{
		LastAttemptAt: &now,
	})
	if status == nil {
		status = &storage.EndpointRuntimeStatus{EndpointName: endpointName, LastAttemptAt: &now, UpdatedAt: now}
	}
	return status
}

func (p *Proxy) recordEndpointSuccess(endpointName string) *storage.EndpointRuntimeStatus {
	now := time.Now().UTC()
	status := p.upsertEndpointRuntimeStatus(endpointName, storage.EndpointRuntimeStatusPatch{
		LastSuccessAt: &now,
	})
	if status == nil {
		status = &storage.EndpointRuntimeStatus{EndpointName: endpointName, LastSuccessAt: &now, UpdatedAt: now}
	}
	return status
}

func endpointFailureStatusCode(statusCodes []int) int {
	if len(statusCodes) == 0 || statusCodes[0] <= 0 {
		return 0
	}
	return statusCodes[0]
}

func (p *Proxy) recordEndpointFailure(endpointName, reason string, statusCodes ...int) *storage.EndpointRuntimeStatus {
	now := time.Now().UTC()
	cleanReason := sanitizeLogField(reason)
	statusCode := endpointFailureStatusCode(statusCodes)
	status := p.upsertEndpointRuntimeStatus(endpointName, storage.EndpointRuntimeStatusPatch{
		LastFailureAt:         &now,
		LastFailureReason:     &cleanReason,
		LastFailureStatusCode: &statusCode,
	})
	if status == nil {
		status = &storage.EndpointRuntimeStatus{
			EndpointName:          endpointName,
			LastFailureAt:         &now,
			LastFailureReason:     cleanReason,
			LastFailureStatusCode: statusCode,
			UpdatedAt:             now,
		}
	}
	p.recordEndpointCircuitFailure(endpointName, cleanReason, statusCode)
	return status
}

func (p *Proxy) recordEndpointError(endpointName, reason string, statusCodes ...int) {
	p.recordEndpointErrorForClient(endpointName, reason, "unknown", statusCodes...)
}

func (p *Proxy) recordEndpointErrorForClient(endpointName, reason, clientIP string, statusCodes ...int) {
	p.stats.RecordErrorForClient(endpointName, clientIP)
	status := p.recordEndpointFailure(endpointName, reason, statusCodes...)
	p.recordEndpointErrorTelemetry(endpointName, reason, endpointFailureStatusCode(statusCodes))
	p.emitEndpointRuntimeEvent(endpointName, "failure", status)
}

func (p *Proxy) recordEndpointSuccessEvent(endpointName string) {
	p.recordEndpointCircuitSuccess(endpointName)
	status := p.recordEndpointSuccess(endpointName)
	p.emitEndpointRuntimeEvent(endpointName, "success", status)
	if p.onEndpointSuccess != nil {
		p.onEndpointSuccess(endpointName)
	}
}

func (p *Proxy) emitCurrentEndpointChanged(previousName, name, reason string) {
	if p.onCurrentEndpointChanged == nil || previousName == name {
		return
	}
	p.onCurrentEndpointChanged(EndpointCurrentEvent{
		Name:         name,
		PreviousName: previousName,
		Reason:       reason,
	})
}

func (p *Proxy) recordEndpointErrorTelemetry(endpointName, reason string, statusCode int) {
	if p == nil || p.storage == nil || strings.TrimSpace(endpointName) == "" {
		return
	}
	endpoint, ok := p.endpointConfigByName(endpointName)
	if !ok {
		endpoint = config.Endpoint{Name: endpointName}
	}
	now := time.Now().UTC()
	windowStart := now.Truncate(5 * time.Minute)
	record := &storage.EndpointErrorStatRecord{
		EndpointName:        endpoint.Name,
		EndpointFingerprint: endpointTelemetryFingerprint(endpoint),
		APIHost:             endpointTelemetryHost(endpoint.APIUrl),
		APIURLFingerprint:   endpointTelemetryURLFingerprint(endpoint.APIUrl),
		AuthMode:            config.NormalizeAuthMode(endpoint.AuthMode),
		Transformer:         endpoint.Transformer,
		Model:               endpoint.Model,
		Reason:              reason,
		StatusCode:          statusCode,
		WindowStart:         windowStart,
		WindowEnd:           windowStart.Add(5 * time.Minute),
		FirstAt:             now,
		LastAt:              now,
		Count:               1,
		Sample:              endpointTelemetrySample(reason, statusCode, endpoint.APIUrl),
	}
	if err := p.storage.RecordEndpointErrorStat(record); err != nil {
		logger.Warn("Failed to record endpoint error telemetry endpoint=%s reason=%s: %v", endpointName, reason, err)
	}
}

func (p *Proxy) endpointConfigByName(endpointName string) (config.Endpoint, bool) {
	if p == nil || p.config == nil {
		return config.Endpoint{}, false
	}
	for _, endpoint := range p.config.GetEndpoints() {
		if endpoint.Name == endpointName {
			return endpoint, true
		}
	}
	return config.Endpoint{}, false
}

func endpointTelemetryFingerprint(endpoint config.Endpoint) string {
	return shortSHA256(strings.Join([]string{
		strings.TrimSpace(endpoint.Name),
		strings.TrimSpace(endpoint.APIUrl),
		config.NormalizeAuthMode(endpoint.AuthMode),
		strings.TrimSpace(endpoint.Transformer),
		strings.TrimSpace(endpoint.Model),
	}, "\x00"))
}

func endpointTelemetryURLFingerprint(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return shortSHA256(strings.TrimSpace(rawURL))
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return shortSHA256(parsed.String())
}

func endpointTelemetryHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return parsed.Host
}

func endpointTelemetrySample(reason string, statusCode int, rawURL string) string {
	parts := []string{"reason=" + strings.TrimSpace(reason)}
	if statusCode > 0 {
		parts = append(parts, "status="+strings.TrimSpace(httpStatusCodeString(statusCode)))
	}
	if host := endpointTelemetryHost(rawURL); host != "" {
		parts = append(parts, "host="+host)
	}
	return storage.SanitizeEndpointErrorSample(strings.Join(parts, " "))
}

func httpStatusCodeString(statusCode int) string {
	if statusCode <= 0 {
		return ""
	}
	return strconv.Itoa(statusCode)
}

func shortSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}
