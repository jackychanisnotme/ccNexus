package onlinelicense

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	EndpointStabilityPeriodDaily   EndpointStabilityPeriod = "daily"
	EndpointStabilityPeriodMonthly EndpointStabilityPeriod = "monthly"

	EndpointStabilityDailyMinimumRequests   = 20
	EndpointStabilityMonthlyMinimumRequests = 100

	EndpointStabilityFindingInsufficientSample      EndpointStabilityFindingCategory = "insufficient_sample"
	EndpointStabilityFindingSupplierIssue           EndpointStabilityFindingCategory = "supplier_issue"
	EndpointStabilityFindingCustomerNetwork         EndpointStabilityFindingCategory = "customer_network"
	EndpointStabilityFindingCustomerConfigOrAccount EndpointStabilityFindingCategory = "customer_config_or_account"
	EndpointStabilityFindingUnknown                 EndpointStabilityFindingCategory = "unknown"
)

type EndpointStabilityPeriod string

type EndpointStabilityFindingCategory string

// EndpointUsageWindow is the minimal aggregate consumed by the deterministic
// scoring engine. Storage records are projected into this privacy-safe shape.
type EndpointUsageWindow struct {
	EndpointFingerprint string    `json:"endpointFingerprint,omitempty"`
	WindowStart         time.Time `json:"windowStart,omitempty"`
	WindowEnd           time.Time `json:"windowEnd,omitempty"`
	Requests            int       `json:"requests"`
	Errors              int       `json:"errors"`
	InputTokens         int       `json:"inputTokens,omitempty"`
	OutputTokens        int       `json:"outputTokens,omitempty"`
}

type AggregateUsageWindow = EndpointUsageWindow

// EndpointErrorWindow is a sanitized aggregate failure window. Sample text is
// deliberately excluded: stability analysis must not depend on customer data.
type EndpointErrorWindow struct {
	EndpointFingerprint string    `json:"endpointFingerprint,omitempty"`
	WindowStart         time.Time `json:"windowStart,omitempty"`
	WindowEnd           time.Time `json:"windowEnd,omitempty"`
	Reason              string    `json:"reason"`
	StatusCode          int       `json:"statusCode,omitempty"`
	Count               int       `json:"count"`
}

type EndpointStabilityAnalysisInput struct {
	Period       EndpointStabilityPeriod `json:"period"`
	UsageWindows []EndpointUsageWindow   `json:"usageWindows"`
	ErrorWindows []EndpointErrorWindow   `json:"errorWindows"`
}

type EndpointStabilityFinding struct {
	Category  EndpointStabilityFindingCategory `json:"category"`
	Severity  string                           `json:"severity"`
	Count     int                              `json:"count"`
	ErrorRate float64                          `json:"errorRate"`
	Reasons   []string                         `json:"reasons,omitempty"`
	Message   string                           `json:"message"`
}

type EndpointStabilityAnalysis struct {
	Period           EndpointStabilityPeriod    `json:"period"`
	SupplierScore    int                        `json:"supplierScore"`
	SampleRequests   int                        `json:"sampleRequests"`
	MinimumRequests  int                        `json:"minimumRequests"`
	SufficientSample bool                       `json:"sufficientSample"`
	TotalErrors      int                        `json:"totalErrors"`
	ClassifiedErrors int                        `json:"classifiedErrors"`
	Findings         []EndpointStabilityFinding `json:"findings"`
}

type endpointStabilityFindingAccumulator struct {
	count   int
	reasons map[string]struct{}
}

var endpointStabilityFindingOrder = []EndpointStabilityFindingCategory{
	EndpointStabilityFindingSupplierIssue,
	EndpointStabilityFindingCustomerNetwork,
	EndpointStabilityFindingCustomerConfigOrAccount,
	EndpointStabilityFindingUnknown,
}

// AnalyzeEndpointStability deterministically scores supplier stability and
// attributes aggregate endpoint failures. It performs no I/O and reads no
// process state, so identical inputs always produce identical output.
func AnalyzeEndpointStability(input EndpointStabilityAnalysisInput) EndpointStabilityAnalysis {
	period := normalizeEndpointStabilityPeriod(input.Period)
	minimumRequests := endpointStabilityMinimumRequests(period)
	sampleRequests, totalErrors := aggregateEndpointUsageWindows(input.UsageWindows)
	accumulators := make(map[EndpointStabilityFindingCategory]*endpointStabilityFindingAccumulator, len(endpointStabilityFindingOrder))
	classifiedErrors := 0

	for _, window := range input.ErrorWindows {
		count := nonNegativeInt(window.Count)
		if count == 0 {
			continue
		}
		category := classifyEndpointStabilityError(window.Reason, window.StatusCode)
		accumulator := accumulators[category]
		if accumulator == nil {
			accumulator = &endpointStabilityFindingAccumulator{reasons: make(map[string]struct{})}
			accumulators[category] = accumulator
		}
		accumulator.count += count
		accumulator.reasons[normalizeEndpointStabilityReason(window.Reason, window.StatusCode)] = struct{}{}
		classifiedErrors += count
	}

	if residual := totalErrors - classifiedErrors; residual > 0 {
		accumulator := accumulators[EndpointStabilityFindingUnknown]
		if accumulator == nil {
			accumulator = &endpointStabilityFindingAccumulator{reasons: make(map[string]struct{})}
			accumulators[EndpointStabilityFindingUnknown] = accumulator
		}
		accumulator.count += residual
		accumulator.reasons["unattributed_usage_error"] = struct{}{}
		classifiedErrors += residual
	}

	supplierErrors := 0
	if accumulator := accumulators[EndpointStabilityFindingSupplierIssue]; accumulator != nil {
		supplierErrors = accumulator.count
	}
	result := EndpointStabilityAnalysis{
		Period:           period,
		SupplierScore:    endpointSupplierScore(sampleRequests, supplierErrors),
		SampleRequests:   sampleRequests,
		MinimumRequests:  minimumRequests,
		SufficientSample: sampleRequests >= minimumRequests,
		TotalErrors:      maxInt(totalErrors, classifiedErrors),
		ClassifiedErrors: classifiedErrors,
		Findings:         make([]EndpointStabilityFinding, 0, len(endpointStabilityFindingOrder)+1),
	}
	if !result.SufficientSample {
		result.Findings = append(result.Findings, EndpointStabilityFinding{
			Category:  EndpointStabilityFindingInsufficientSample,
			Severity:  "info",
			Count:     sampleRequests,
			ErrorRate: 0,
			Message:   fmt.Sprintf("insufficient sample: %d requests observed, %d required for %s analysis", sampleRequests, minimumRequests, period),
		})
	}
	for _, category := range endpointStabilityFindingOrder {
		accumulator := accumulators[category]
		if accumulator == nil || accumulator.count == 0 {
			continue
		}
		reasons := sortedEndpointStabilityReasons(accumulator.reasons)
		rate := endpointStabilityRate(accumulator.count, sampleRequests)
		result.Findings = append(result.Findings, EndpointStabilityFinding{
			Category:  category,
			Severity:  endpointStabilitySeverity(rate),
			Count:     accumulator.count,
			ErrorRate: rate,
			Reasons:   reasons,
			Message:   endpointStabilityFindingMessage(category, accumulator.count, rate),
		})
	}
	return result
}

// AnalyzeSupplierStability is a descriptive alias for callers focused on the
// supplier score rather than the complete finding set.
func AnalyzeSupplierStability(input EndpointStabilityAnalysisInput) EndpointStabilityAnalysis {
	return AnalyzeEndpointStability(input)
}

func normalizeEndpointStabilityPeriod(period EndpointStabilityPeriod) EndpointStabilityPeriod {
	if strings.EqualFold(strings.TrimSpace(string(period)), string(EndpointStabilityPeriodMonthly)) {
		return EndpointStabilityPeriodMonthly
	}
	return EndpointStabilityPeriodDaily
}

func endpointStabilityMinimumRequests(period EndpointStabilityPeriod) int {
	if period == EndpointStabilityPeriodMonthly {
		return EndpointStabilityMonthlyMinimumRequests
	}
	return EndpointStabilityDailyMinimumRequests
}

func aggregateEndpointUsageWindows(windows []EndpointUsageWindow) (int, int) {
	requests := 0
	errorsCount := 0
	for _, window := range windows {
		requests += nonNegativeInt(window.Requests)
		errorsCount += nonNegativeInt(window.Errors)
	}
	return requests, errorsCount
}

func classifyEndpointStabilityError(reason string, statusCode int) EndpointStabilityFindingCategory {
	normalized := normalizeEndpointStabilityReason(reason, statusCode)
	switch normalized {
	case "rate_limited", "upstream_5xx", "retryable_status", "upstream_stream_error",
		"streaming_failed", "route_unavailable", "upstream_route_unavailable",
		"semantic_empty_response", "missing_response_done", "transport_protocol_error":
		return EndpointStabilityFindingSupplierIssue
	case "transient_network_error", "send_request_failed", "connection_error",
		"connection_reset", "dns_error", "network_error", "network_timeout",
		"proxy_error", "tls_error", "timeout":
		return EndpointStabilityFindingCustomerNetwork
	case "authentication_error", "authorization_error", "invalid_api_key",
		"invalid_request", "invalid_request_error", "quota_exhausted",
		"credential_auth_failed", "no_usable_token", "account_disabled",
		"account_error", "permission_denied":
		return EndpointStabilityFindingCustomerConfigOrAccount
	}
	if statusCode == 429 || statusCode >= 500 {
		return EndpointStabilityFindingSupplierIssue
	}
	if statusCode == 400 || statusCode == 401 || statusCode == 402 || statusCode == 403 ||
		statusCode == 404 || statusCode == 409 || statusCode == 422 {
		return EndpointStabilityFindingCustomerConfigOrAccount
	}
	return EndpointStabilityFindingUnknown
}

func normalizeEndpointStabilityReason(reason string, statusCode int) string {
	normalized := strings.ToLower(strings.TrimSpace(reason))
	normalized = strings.NewReplacer("-", "_", " ", "_", ".", "_", "/", "_").Replace(normalized)
	normalized = strings.Trim(normalized, "_")
	for strings.Contains(normalized, "__") {
		normalized = strings.ReplaceAll(normalized, "__", "_")
	}
	if normalized != "" {
		return normalized
	}
	if statusCode > 0 {
		return fmt.Sprintf("http_%d", statusCode)
	}
	return "unknown"
}

func endpointSupplierScore(requests, supplierErrors int) int {
	if requests <= 0 {
		return 0
	}
	errorsCount := minInt(nonNegativeInt(supplierErrors), requests)
	return int(math.Round(100 * (1 - float64(errorsCount)/float64(requests))))
}

func endpointStabilityRate(count, requests int) float64 {
	if count <= 0 || requests <= 0 {
		return 0
	}
	rate := 100 * float64(count) / float64(requests)
	if rate > 100 {
		rate = 100
	}
	return math.Round(rate*100) / 100
}

func endpointStabilitySeverity(rate float64) string {
	switch {
	case rate >= 20:
		return "critical"
	case rate >= 5:
		return "high"
	case rate >= 1:
		return "medium"
	default:
		return "low"
	}
}

func endpointStabilityFindingMessage(category EndpointStabilityFindingCategory, count int, rate float64) string {
	switch category {
	case EndpointStabilityFindingSupplierIssue:
		return fmt.Sprintf("supplier-attributable failures: %d (%.2f%% of requests)", count, rate)
	case EndpointStabilityFindingCustomerNetwork:
		return fmt.Sprintf("customer network failures: %d (%.2f%% of requests)", count, rate)
	case EndpointStabilityFindingCustomerConfigOrAccount:
		return fmt.Sprintf("customer configuration or account failures: %d (%.2f%% of requests)", count, rate)
	default:
		return fmt.Sprintf("unclassified failures: %d (%.2f%% of requests)", count, rate)
	}
}

func sortedEndpointStabilityReasons(values map[string]struct{}) []string {
	reasons := make([]string, 0, len(values))
	for reason := range values {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	return reasons
}

func nonNegativeInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
