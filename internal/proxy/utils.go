package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/tokencount"
)

// normalizeAPIUrl ensures the API URL has a protocol prefix
func normalizeAPIUrl(apiUrl string) string {
	if !strings.HasPrefix(apiUrl, "http://") && !strings.HasPrefix(apiUrl, "https://") {
		return "https://" + apiUrl
	}
	return apiUrl
}

func cloneEndpoints(endpoints []config.Endpoint) []config.Endpoint {
	if len(endpoints) == 0 {
		return nil
	}
	cloned := make([]config.Endpoint, len(endpoints))
	copy(cloned, endpoints)
	return cloned
}

const (
	endpointFastFailoverAttempts = 2
	endpointSlowFailoverAttempts = 3

	// Disabled by default; the HTTP transport still enforces its 90s ResponseHeaderTimeout.
	defaultStreamHeaderTimeout     = 0 * time.Second
	defaultStreamHeartbeatInterval = 10 * time.Second
	retryReasonEndpointAuthFailed  = "endpoint_auth_failed"
	retryReasonTransportProtocol   = "transport_protocol_error"
)

func (p *Proxy) streamHeaderTimeoutOrDefault() time.Duration {
	if p != nil && p.streamHeaderTimeout > 0 {
		return p.streamHeaderTimeout
	}
	return defaultStreamHeaderTimeout
}

func (p *Proxy) streamHeartbeatIntervalOrDefault() time.Duration {
	if p != nil && p.streamHeartbeatInterval > 0 {
		return p.streamHeartbeatInterval
	}
	return defaultStreamHeartbeatInterval
}

// shouldRetry determines if a response should trigger a retry
func shouldRetry(statusCode int) bool {
	return statusCode != http.StatusOK &&
		statusCode != http.StatusBadRequest &&
		statusCode != http.StatusUnauthorized
}

func isClientCanceled(ctx context.Context, err error) bool {
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	if err == nil {
		return false
	}
	if isResponseHeaderTimeoutError(err) {
		return false
	}
	return errors.Is(err, context.Canceled) ||
		strings.Contains(strings.ToLower(err.Error()), "context canceled")
}

func isResponseHeaderTimeoutError(err error) bool {
	var target responseHeaderTimeoutError
	return errors.As(err, &target)
}

func retryReasonForRequestError(err error) string {
	if isTransportProtocolError(err) {
		return retryReasonTransportProtocol
	}
	if isTransientNetworkError(err) {
		return "transient_network_error"
	}
	return "send_request_failed"
}

func isRetryableRequestErrorReason(reason string) bool {
	switch sanitizeLogField(reason) {
	case "transient_network_error", retryReasonTransportProtocol:
		return true
	default:
		return false
	}
}

func isTransportProtocolError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if !strings.Contains(message, "malformed http response") {
		return false
	}
	return strings.Contains(message, "\\x00") ||
		strings.Contains(message, "\x00") ||
		strings.Contains(message, "\\x04") ||
		strings.Contains(message, "\\x08")
}

// retryReasonForHTTPStatus classifies upstream HTTP retry failures for logs and
// endpoint rotation policy. Upstream gateways sometimes wrap rate limits inside
// HTTP 500 bodies, so inspect the body in addition to the status code.
func retryReasonForHTTPStatus(statusCode int, body string) string {
	if isQuotaExhaustedHTTPFailure(statusCode, body) {
		return "quota_exhausted"
	}
	if isRateLimitedHTTPFailure(statusCode, body) {
		return "rate_limited"
	}
	if statusCode >= http.StatusInternalServerError {
		return "upstream_5xx"
	}
	return "retryable_status"
}

func shouldRetryWithForcedStream(statusCode int, body string, clientRequestedStream bool, transformerName string) bool {
	if statusCode < http.StatusBadRequest || clientRequestedStream {
		return false
	}
	if !strings.Contains(strings.ToLower(strings.TrimSpace(transformerName)), "openai2") {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(body))
	return strings.Contains(lower, "stream must be set to true")
}

func isUpstreamInvalidRequestHTTPFailure(statusCode int, body string) bool {
	if statusCode < http.StatusInternalServerError {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(body))
	return strings.Contains(lower, `"code":"invalid_request"`) ||
		strings.Contains(lower, `"type":"invalid_request_error"`) ||
		strings.Contains(lower, "invalid_request")
}

func shouldRotateEndpointAfterHTTPFailure(endpointAttempts int, statusCode int, body string) bool {
	return endpointAttempts >= failoverAttemptsForHTTPFailure(statusCode, body)
}

func failoverAttemptsForHTTPFailure(statusCode int, body string) int {
	if isQuotaExhaustedHTTPFailure(statusCode, body) {
		return 1
	}
	// HTTP upstream failures are usually provider pressure or gateway hiccups.
	// Try them a little longer before globally rotating endpoints.
	if statusCode > 0 {
		return endpointSlowFailoverAttempts
	}
	return endpointFastFailoverAttempts
}

func isQuotaExhaustedHTTPFailure(statusCode int, body string) bool {
	if statusCode == 0 {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(body))
	if strings.Contains(lower, "insufficient_user_quota") ||
		strings.Contains(lower, "insufficient_quota") ||
		strings.Contains(lower, "quota_exhausted") ||
		strings.Contains(lower, "quota exhausted") ||
		strings.Contains(lower, "exceeded your current quota") ||
		strings.Contains(lower, "用户额度不足") ||
		strings.Contains(lower, "余额不足") {
		return true
	}
	return strings.Contains(lower, "剩余额度") &&
		(strings.Contains(lower, "0.000000") || strings.Contains(lower, "￥0") || strings.Contains(lower, "$0") || strings.Contains(lower, "＄0"))
}

func isRateLimitedHTTPFailure(statusCode int, body string) bool {
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(body))
	return strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "rate_limit") ||
		strings.Contains(lower, "429")
}

func shouldTreatAPIKeyEndpointAuthFailure(authMode string, statusCode int, body string) bool {
	if config.NormalizeAuthMode(authMode) != config.AuthModeAPIKey {
		return false
	}
	if statusCode == http.StatusUnauthorized {
		return true
	}
	if statusCode != http.StatusForbidden {
		return false
	}

	lower := strings.ToLower(strings.TrimSpace(body))
	if lower == "" {
		return false
	}
	authMarkers := []string{
		"invalid token",
		"invalid_token",
		"invalid api key",
		"invalid_api_key",
		"invalid apikey",
		"invalid authentication",
		"invalid auth",
		"invalid credential",
		"unauthorized",
		"unauthenticated",
		"authentication failed",
		"authorization failed",
		"api key",
		"api_key",
		"apikey",
		"access token",
		"bearer token",
		"token expired",
		"expired token",
	}
	for _, marker := range authMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// cleanIncompleteToolCalls removes incomplete tool_use blocks from request
func cleanIncompleteToolCalls(bodyBytes []byte) ([]byte, error) {
	var req map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		return bodyBytes, err
	}

	messages, ok := req["messages"].([]interface{})
	if !ok {
		return bodyBytes, nil
	}

	hasIncomplete := false
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(map[string]interface{})
		if !ok {
			continue
		}

		role, _ := msg["role"].(string)
		if role != "assistant" {
			break
		}

		content, ok := msg["content"].([]interface{})
		if !ok {
			break
		}

		var cleanedContent []interface{}
		for _, block := range content {
			blockMap, ok := block.(map[string]interface{})
			if !ok {
				cleanedContent = append(cleanedContent, block)
				continue
			}

			blockType, _ := blockMap["type"].(string)
			if blockType == "tool_use" {
				if input, hasInput := blockMap["input"]; !hasInput || input == nil {
					logger.Debug("Removing incomplete tool_use block without input")
					hasIncomplete = true
					continue
				}
			}
			cleanedContent = append(cleanedContent, block)
		}

		if hasIncomplete {
			if len(cleanedContent) == 0 {
				messages = append(messages[:i], messages[i+1:]...)
			} else {
				msg["content"] = cleanedContent
			}
		}
		break
	}

	if !hasIncomplete {
		return bodyBytes, nil
	}

	req["messages"] = messages
	return json.Marshal(req)
}

// estimateInputTokens estimates input tokens from request body
func (p *Proxy) estimateInputTokens(bodyBytes []byte) int {
	var req tokencount.CountTokensRequest
	if json.Unmarshal(bodyBytes, &req) == nil {
		return tokencount.EstimateInputTokens(&req)
	}
	return 0
}

// estimateTokens estimates tokens when API doesn't provide usage
func (p *Proxy) estimateTokens(bodyBytes []byte, outputText string, inputTokens, outputTokens int, endpointName string) (int, int) {
	if inputTokens == 0 {
		var req tokencount.CountTokensRequest
		if json.Unmarshal(bodyBytes, &req) == nil {
			inputTokens = tokencount.EstimateInputTokens(&req)
			logger.Debug("[%s] Estimated input tokens: %d", endpointName, inputTokens)
		}
	}

	if outputTokens == 0 && outputText != "" {
		outputTokens = tokencount.EstimateOutputTokens(outputText)
		logger.Debug("[%s] Estimated output tokens: %d", endpointName, outputTokens)
	}

	return inputTokens, outputTokens
}
