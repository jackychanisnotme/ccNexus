package proxy

import (
	"strings"
	"time"

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
