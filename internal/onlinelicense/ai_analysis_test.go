package onlinelicense

import (
	"reflect"
	"testing"
)

func TestAnalyzeEndpointStabilitySampleThresholds(t *testing.T) {
	tests := []struct {
		name       string
		period     EndpointStabilityPeriod
		requests   int
		wantMin    int
		wantEnough bool
	}{
		{name: "daily below", period: EndpointStabilityPeriodDaily, requests: 19, wantMin: 20, wantEnough: false},
		{name: "daily exact", period: EndpointStabilityPeriodDaily, requests: 20, wantMin: 20, wantEnough: true},
		{name: "monthly below", period: EndpointStabilityPeriodMonthly, requests: 99, wantMin: 100, wantEnough: false},
		{name: "monthly exact", period: EndpointStabilityPeriodMonthly, requests: 100, wantMin: 100, wantEnough: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := AnalyzeEndpointStability(EndpointStabilityAnalysisInput{
				Period:       test.period,
				UsageWindows: []EndpointUsageWindow{{Requests: test.requests}},
			})
			if result.MinimumRequests != test.wantMin || result.SufficientSample != test.wantEnough {
				t.Fatalf("threshold result = min %d enough %v, want min %d enough %v", result.MinimumRequests, result.SufficientSample, test.wantMin, test.wantEnough)
			}
			hasInsufficient := findingByCategory(result.Findings, EndpointStabilityFindingInsufficientSample) != nil
			if hasInsufficient == test.wantEnough {
				t.Fatalf("insufficient finding present = %v, sufficient = %v", hasInsufficient, test.wantEnough)
			}
		})
	}
}

func TestAnalyzeEndpointStabilityClassifiesFailuresAndScoresSupplier(t *testing.T) {
	input := EndpointStabilityAnalysisInput{
		Period: EndpointStabilityPeriodMonthly,
		UsageWindows: []EndpointUsageWindow{
			{Requests: 60, Errors: 7},
			{Requests: 40, Errors: 4},
		},
		ErrorWindows: []EndpointErrorWindow{
			{Reason: "upstream_5xx", StatusCode: 503, Count: 4},
			{Reason: "Rate Limited", StatusCode: 429, Count: 1},
			{Reason: "transient-network-error", Count: 2},
			{Reason: "invalid_api_key", StatusCode: 401, Count: 3},
			{Reason: "new_failure_reason", Count: 1},
		},
	}

	result := AnalyzeEndpointStability(input)

	if result.SupplierScore != 95 {
		t.Fatalf("supplier score = %d, want 95", result.SupplierScore)
	}
	if !result.SufficientSample || result.SampleRequests != 100 || result.TotalErrors != 11 || result.ClassifiedErrors != 11 {
		t.Fatalf("unexpected totals: %#v", result)
	}
	assertFinding(t, result.Findings, EndpointStabilityFindingSupplierIssue, 5, 5, "high", []string{"rate_limited", "upstream_5xx"})
	assertFinding(t, result.Findings, EndpointStabilityFindingCustomerNetwork, 2, 2, "medium", []string{"transient_network_error"})
	assertFinding(t, result.Findings, EndpointStabilityFindingCustomerConfigOrAccount, 3, 3, "medium", []string{"invalid_api_key"})
	assertFinding(t, result.Findings, EndpointStabilityFindingUnknown, 1, 1, "medium", []string{"new_failure_reason"})
}

func TestAnalyzeEndpointStabilityAttributesUnmatchedUsageErrorsAsUnknown(t *testing.T) {
	result := AnalyzeEndpointStability(EndpointStabilityAnalysisInput{
		UsageWindows: []EndpointUsageWindow{{Requests: 20, Errors: 5}},
		ErrorWindows: []EndpointErrorWindow{
			{Reason: "quota_exhausted", StatusCode: 402, Count: 2},
		},
	})

	if result.SupplierScore != 100 {
		t.Fatalf("supplier score = %d, want customer failures not to reduce score", result.SupplierScore)
	}
	assertFinding(t, result.Findings, EndpointStabilityFindingCustomerConfigOrAccount, 2, 10, "high", []string{"quota_exhausted"})
	assertFinding(t, result.Findings, EndpointStabilityFindingUnknown, 3, 15, "high", []string{"unattributed_usage_error"})
}

func TestAnalyzeEndpointStabilityUsesStatusFallbackAndStableFindingOrder(t *testing.T) {
	input := EndpointStabilityAnalysisInput{
		UsageWindows: []EndpointUsageWindow{{Requests: 20, Errors: 0}},
		ErrorWindows: []EndpointErrorWindow{
			{StatusCode: 418, Count: 1},
			{StatusCode: 401, Count: 1},
			{StatusCode: 503, Count: 1},
			{Reason: "dns error", Count: 1},
			{Reason: "upstream_5xx", Count: -10},
		},
	}

	first := AnalyzeEndpointStability(input)
	second := AnalyzeEndpointStability(input)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("identical inputs produced different results:\nfirst=%#v\nsecond=%#v", first, second)
	}
	gotOrder := make([]EndpointStabilityFindingCategory, 0, len(first.Findings))
	for _, finding := range first.Findings {
		gotOrder = append(gotOrder, finding.Category)
	}
	wantOrder := []EndpointStabilityFindingCategory{
		EndpointStabilityFindingSupplierIssue,
		EndpointStabilityFindingCustomerNetwork,
		EndpointStabilityFindingCustomerConfigOrAccount,
		EndpointStabilityFindingUnknown,
	}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("finding order = %#v, want %#v", gotOrder, wantOrder)
	}
	if first.SupplierScore != 95 {
		t.Fatalf("supplier score = %d, want 95", first.SupplierScore)
	}
}

func TestAnalyzeEndpointStabilityDefaultsUnknownPeriodToDaily(t *testing.T) {
	result := AnalyzeSupplierStability(EndpointStabilityAnalysisInput{
		Period:       "unexpected",
		UsageWindows: []EndpointUsageWindow{{Requests: -1, Errors: -2}},
	})

	if result.Period != EndpointStabilityPeriodDaily || result.MinimumRequests != 20 {
		t.Fatalf("period = %q min = %d, want daily min 20", result.Period, result.MinimumRequests)
	}
	if result.SampleRequests != 0 || result.TotalErrors != 0 || result.SupplierScore != 0 {
		t.Fatalf("negative aggregates were not normalized: %#v", result)
	}
}

func assertFinding(t *testing.T, findings []EndpointStabilityFinding, category EndpointStabilityFindingCategory, count int, rate float64, severity string, reasons []string) {
	t.Helper()
	finding := findingByCategory(findings, category)
	if finding == nil {
		t.Fatalf("missing finding category %q in %#v", category, findings)
	}
	if finding.Count != count || finding.ErrorRate != rate || finding.Severity != severity || !reflect.DeepEqual(finding.Reasons, reasons) {
		t.Fatalf("finding %q = %#v, want count=%d rate=%v severity=%s reasons=%#v", category, finding, count, rate, severity, reasons)
	}
}

func findingByCategory(findings []EndpointStabilityFinding, category EndpointStabilityFindingCategory) *EndpointStabilityFinding {
	for index := range findings {
		if findings[index].Category == category {
			return &findings[index]
		}
	}
	return nil
}
