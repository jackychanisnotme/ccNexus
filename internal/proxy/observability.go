package proxy

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/lich0821/ccNexus/internal/logger"
)

const (
	headerAINexusRequestID = "X-AINexus-Request-ID"
	headerAINexusEndpoint  = "X-AINexus-Endpoint"
	headerAINexusAttempt   = "X-AINexus-Attempt"
	headerCCNexusRequestID = "X-ccNexus-Request-ID"
	headerCCNexusEndpoint  = "X-ccNexus-Endpoint"
	headerCCNexusAttempt   = "X-ccNexus-Attempt"
)

type requestObservability struct {
	RequestID string
	ClientIP  string
	Agent     string
}

func newRequestObservability(r *http.Request) requestObservability {
	requestID := firstNonEmptyHeader(r.Header, headerAINexusRequestID, headerCCNexusRequestID, "X-Request-ID", "X-Correlation-ID")
	if requestID == "" {
		requestID = uuid.NewString()
	}

	return requestObservability{
		RequestID: sanitizeLogField(requestID),
		ClientIP:  sanitizeLogField(extractClientIP(r)),
		Agent:     sanitizeLogField(extractAgentHeader(r)),
	}
}

func applyRequestObservabilityHeaders(w http.ResponseWriter, obs requestObservability, endpointName string, attempt int) {
	if obs.RequestID != "" {
		w.Header().Set(headerAINexusRequestID, obs.RequestID)
		w.Header().Set(headerCCNexusRequestID, obs.RequestID)
	}
	if strings.TrimSpace(endpointName) != "" {
		w.Header().Set(headerAINexusEndpoint, endpointName)
		w.Header().Set(headerCCNexusEndpoint, endpointName)
	}
	if attempt > 0 {
		value := strconv.Itoa(attempt)
		w.Header().Set(headerAINexusAttempt, value)
		w.Header().Set(headerCCNexusAttempt, value)
	}
}

func logRequestAttemptStart(obs requestObservability, endpointName, action, modelName string, requestBytes int, attempt int, proxyLabel string) {
	fields := requestLogFields(obs, endpointName, attempt, 0, "")
	if strings.TrimSpace(proxyLabel) == "" {
		logger.Debug("[%s] %s %s %d %s", endpointName, action, modelName, requestBytes, fields)
		return
	}
	logger.Debug("[%s] %s %s %d %s %s", endpointName, action, modelName, requestBytes, proxyLabel, fields)
}

func logRequestAttemptResult(obs requestObservability, endpointName string, attempt int, upstreamStatus int, retryReason string, format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	fields := requestLogFields(obs, endpointName, attempt, upstreamStatus, retryReason)
	logger.Debug("[%s] %s %s", endpointName, message, fields)
}

func logRequestAttemptWarn(obs requestObservability, endpointName string, attempt int, upstreamStatus int, retryReason string, format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	fields := requestLogFields(obs, endpointName, attempt, upstreamStatus, retryReason)
	logger.Warn("[%s] %s %s", endpointName, message, fields)
}

func logRequestAttemptError(obs requestObservability, endpointName string, attempt int, upstreamStatus int, retryReason string, format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	fields := requestLogFields(obs, endpointName, attempt, upstreamStatus, retryReason)
	logger.Error("[%s] %s %s", endpointName, message, fields)
}

func requestLogFields(obs requestObservability, endpointName string, attempt int, upstreamStatus int, retryReason string) string {
	parts := []string{
		"request_id=" + obs.RequestID,
		"client_ip=" + obs.ClientIP,
		"agent=" + obs.Agent,
	}
	if strings.TrimSpace(endpointName) != "" {
		parts = append(parts, "endpoint="+sanitizeLogField(endpointName))
	}
	if attempt > 0 {
		parts = append(parts, "attempt="+strconv.Itoa(attempt))
	}
	if upstreamStatus > 0 {
		parts = append(parts, "upstream_status="+strconv.Itoa(upstreamStatus))
	}
	if strings.TrimSpace(retryReason) != "" {
		parts = append(parts, "retry_reason="+sanitizeLogField(retryReason))
	}
	return strings.Join(parts, " ")
}

func firstNonEmptyHeader(headers http.Header, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func extractAgentHeader(r *http.Request) string {
	agent := firstNonEmptyHeader(
		r.Header,
		"X-OpenClaw-Agent",
		"X-Agent-ID",
		"X-Agent-Id",
		"X-Agent-Name",
		"X-Claude-Code-Session-Id",
		"X-Claude-Code-Session",
	)
	if agent != "" {
		return agent
	}
	if userAgent := strings.TrimSpace(r.UserAgent()); userAgent != "" {
		return userAgent
	}
	return "unknown"
}

func extractClientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	if host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr)); err == nil && host != "" {
		return host
	}
	if remoteAddr := strings.TrimSpace(r.RemoteAddr); remoteAddr != "" {
		return remoteAddr
	}
	return "unknown"
}

func sanitizeLogField(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return strings.Join(strings.Fields(value), "_")
}
