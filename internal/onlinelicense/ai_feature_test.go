package onlinelicense

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type failingAIProvider struct {
	calls int
}

func (p *failingAIProvider) ListModels(context.Context) ([]string, error) {
	return []string{"analysis-model"}, nil
}

func (p *failingAIProvider) Analyze(context.Context, string, interface{}) (AIModelAnalysis, error) {
	p.calls++
	return AIModelAnalysis{}, errors.New("provider unavailable")
}

func TestRemoteSnapshotCreatesUsageDeltasAndHandlesCounterReset(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "license.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 13, 1, 0, 0, 0, time.UTC)
	if err := store.UpsertRemoteDeviceState(&RemoteDeviceState{
		DeviceID:       "device-a",
		OwnerAccountID: 7,
	}, now); err != nil {
		t.Fatalf("upsert remote device: %v", err)
	}
	base := RemoteSnapshot{Endpoints: []RemoteEndpointSnapshot{{
		Name: "Primary", APIUrl: "https://supplier.example/v1", AuthMode: "api_key",
		Transformer: "openai", Model: "gpt-test",
		Stats: RemoteUsageStats{Requests: 100, Errors: 5, InputTokens: 1000, OutputTokens: 200},
	}}}
	if err := store.UpsertRemoteSnapshot("device-a", base, now); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	next := base
	next.Endpoints = append([]RemoteEndpointSnapshot(nil), base.Endpoints...)
	next.Endpoints[0].Stats = RemoteUsageStats{Requests: 112, Errors: 7, InputTokens: 1300, OutputTokens: 260}
	if err := store.UpsertRemoteSnapshot("device-a", next, now.Add(time.Minute)); err != nil {
		t.Fatalf("write delta: %v", err)
	}
	rows, err := store.ListEndpointUsageWindows(AIQuery{DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("list usage: %v", err)
	}
	if len(rows) != 1 || rows[0].Requests != 12 || rows[0].Errors != 2 ||
		rows[0].InputTokens != 300 || rows[0].OutputTokens != 60 ||
		rows[0].OwnerAccountID != 7 || rows[0].APIHost != "supplier.example" {
		t.Fatalf("usage delta = %#v", rows)
	}

	reset := next
	reset.Endpoints = append([]RemoteEndpointSnapshot(nil), next.Endpoints...)
	reset.Endpoints[0].Stats = RemoteUsageStats{Requests: 1, Errors: 0, InputTokens: 5, OutputTokens: 2}
	if err := store.UpsertRemoteSnapshot("device-a", reset, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("write reset baseline: %v", err)
	}
	rows, err = store.ListEndpointUsageWindows(AIQuery{DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("list after reset: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("counter reset created unexpected window: %#v", rows)
	}
}

func TestCorrelateAIAnalysisDistinguishesSupplierNetworkAndAccountFailures(t *testing.T) {
	now := time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC)
	usage := []EndpointUsageRecord{
		{DeviceID: "a", APIHost: "supplier.example", Requests: 50, WindowEnd: now},
		{DeviceID: "b", APIHost: "supplier.example", Requests: 50, WindowEnd: now},
		{DeviceID: "a", APIHost: "healthy.example", Requests: 50, WindowEnd: now},
		{DeviceID: "b", APIHost: "healthy.example", Requests: 50, WindowEnd: now},
	}
	errorRows := []EndpointErrorTelemetryRecord{
		{DeviceID: "a", APIHost: "supplier.example", Reason: "upstream_5xx", StatusCode: 503, Count: 3},
		{DeviceID: "b", APIHost: "supplier.example", Reason: "upstream_5xx", StatusCode: 503, Count: 2},
		{DeviceID: "a", APIHost: "healthy.example", Reason: "transient_network_error", Count: 4},
		{DeviceID: "a", APIHost: "account.example", Reason: "credential_auth_failed", StatusCode: 401, Count: 1},
	}
	result := correlateAIAnalysis(usage, errorRows, false, now)
	assertClassificationCount(t, result.Findings, AIFindingSupplierIssue, 5)
	assertClassificationCount(t, result.Findings, AIFindingCustomerNetwork, 4)
	assertClassificationCount(t, result.Findings, AIFindingCustomerConfigAccount, 1)
	for _, supplier := range result.Suppliers {
		if supplier.APIHost == "supplier.example" {
			if supplier.Score != 95 || !supplier.SampleSufficient {
				t.Fatalf("supplier summary = %#v", supplier)
			}
			return
		}
	}
	t.Fatal("supplier summary not found")
}

func TestRunAIJobPersistsDeterministicResultWithoutProvider(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "license.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 13, 3, 0, 0, 0, time.UTC)
	service := NewService(store, nil, Options{Now: func() time.Time { return now }})
	job, err := service.queueAIJob(AIJobTypeManual, 0, now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	if err := service.RunAIJob(context.Background(), job.ID); err != nil {
		t.Fatalf("run job: %v", err)
	}
	saved, err := store.GetAIJob(job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if saved.Status != AIJobStatusSkippedNoAIProvider || len(saved.Result) == 0 {
		t.Fatalf("saved job = %#v", saved)
	}
	var result AIAnalysisResult
	if err := json.Unmarshal(saved.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
}

func TestAISettingsAndScopePermissions(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "license.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	service := NewService(store, nil, Options{})
	viewer := &AdminAccount{ID: 2, Level: AdminLevelReseller, Status: AdminAccountStatusActive, Permissions: []string{PermissionAIAnalysisView}}
	if _, err := service.UpdateAISettingsFor(viewer, AISettings{}); err != ErrForbidden {
		t.Fatalf("viewer settings update error = %v, want forbidden", err)
	}
	manager := &AdminAccount{ID: 1, Level: AdminLevelRoot, Status: AdminAccountStatusActive, Permissions: allAdminPermissions()}
	if _, err := service.UpdateAISettingsFor(manager, AISettings{
		Enabled: true, Model: "", Timezone: defaultAITimezone, DailyTime: defaultAIDailyTime,
		MonthlyTime: defaultAIMonthlyTime,
	}); err == nil {
		t.Fatal("enabled settings without model unexpectedly accepted")
	}
}

func TestAIAdminRoutesRequireSessionAndRootCanUseThem(t *testing.T) {
	handler := newTestHTTPHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/ai/settings", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous AI settings status = %d, want 401", rec.Code)
	}
	cookie := loginAdmin(t, handler)
	for _, path := range []string{
		"/api/admin/ai/settings",
		"/api/admin/ai/models",
		"/api/admin/ai/jobs",
		"/api/admin/ai/findings",
		"/api/admin/ai/suppliers/summary",
		"/api/admin/ai/reports",
	} {
		req = httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	req = httptest.NewRequest(http.MethodPut, "/api/admin/ai/settings", strings.NewReader(
		`{"enabled":false,"timezone":"Asia/Shanghai","dailyTime":"02:30","monthlyTime":"09:00","promptVersion":"endpoint-stability-v1"}`,
	))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update AI settings status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAIWindowClippingAndTemporalCorrelation(t *testing.T) {
	from := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	clipped := clipUsageRecords([]EndpointUsageRecord{{
		WindowStart: from.Add(-24 * time.Hour),
		WindowEnd:   to,
		Requests:    100,
		Errors:      10,
	}}, from, to)
	if len(clipped) != 1 || clipped[0].Requests != 50 || clipped[0].Errors != 5 {
		t.Fatalf("clipped usage = %#v", clipped)
	}

	usage := []EndpointUsageRecord{
		{DeviceID: "a", APIHost: "supplier.example", Requests: 50, WindowStart: from, WindowEnd: to},
		{DeviceID: "b", APIHost: "supplier.example", Requests: 50, WindowStart: from, WindowEnd: to},
	}
	result := correlateAIAnalysis(usage, []EndpointErrorTelemetryRecord{
		{DeviceID: "a", APIHost: "supplier.example", Reason: "upstream_5xx", StatusCode: 503, Count: 2, WindowStart: from},
		{DeviceID: "b", APIHost: "supplier.example", Reason: "credential_auth_failed", StatusCode: 401, Count: 1, WindowStart: from},
		{DeviceID: "a", APIHost: "supplier.example", Reason: "transient_network_error", Count: 2, WindowStart: from},
		{DeviceID: "b", APIHost: "supplier.example", Reason: "transient_network_error", Count: 2, WindowStart: from.Add(time.Hour)},
	}, false, to)
	assertClassificationCount(t, result.Findings, AIFindingSupplierIssue, 0)
	assertClassificationCount(t, result.Findings, AIFindingUnknown, 2)
	assertClassificationCount(t, result.Findings, AIFindingCustomerNetwork, 4)
	assertClassificationCount(t, result.Findings, AIFindingCustomerConfigAccount, 1)
}

func TestAIJobLimitsAtomicClaimAndProviderRetryState(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "license.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 13, 4, 0, 0, 0, time.UTC)
	provider := &failingAIProvider{}
	service := NewService(store, nil, Options{Now: func() time.Time { return now }, AIProvider: provider})
	root := &AdminAccount{ID: 1, Level: AdminLevelRoot, Status: AdminAccountStatusActive, Permissions: allAdminPermissions()}
	if _, err := service.QueueAIJobFor(root, AIJobRequest{
		From: now.Add(-32 * 24 * time.Hour), To: now,
	}); err == nil {
		t.Fatal("oversized AI window unexpectedly accepted")
	}
	if _, err := service.QueueAIJobFor(root, AIJobRequest{
		From: now, To: now.Add(time.Hour),
	}); err == nil {
		t.Fatal("future AI window unexpectedly accepted")
	}
	if err := store.SaveAISettings(AISettings{
		Enabled: true, Model: "analysis-model", Timezone: defaultAITimezone,
		DailyTime: defaultAIDailyTime, MonthlyTime: defaultAIMonthlyTime,
		PromptVersion: defaultAIPromptVersion,
	}, now); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	job, err := service.QueueAIJobFor(root, AIJobRequest{
		From: now.Add(-24 * time.Hour), To: now,
	})
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	if err := service.RunAIJob(context.Background(), job.ID); err == nil {
		t.Fatal("provider failure unexpectedly returned success")
	}
	saved, err := store.GetAIJob(job.ID)
	if err != nil {
		t.Fatalf("get failed job: %v", err)
	}
	if saved.Status != AIJobStatusFailed || saved.Attempts != 1 || len(saved.Result) == 0 {
		t.Fatalf("failed provider job = %#v", saved)
	}
	for attempt := 1; attempt < maxAIJobAttempts; attempt++ {
		_ = service.RunAIJob(context.Background(), job.ID)
	}
	_ = service.RunAIJob(context.Background(), job.ID)
	saved, _ = store.GetAIJob(job.ID)
	if saved.Attempts != maxAIJobAttempts || provider.calls != maxAIJobAttempts {
		t.Fatalf("retry attempts=%d provider calls=%d", saved.Attempts, provider.calls)
	}
}

func TestAIJobCapacityAndSpreadsheetCellEscaping(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "license.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 13, 5, 0, 0, 0, time.UTC)
	service := NewService(store, nil, Options{Now: func() time.Time { return now }})
	for index := 0; index < 2; index++ {
		_, err := service.queueAIJob(AIJobTypeManual, 9, now.Add(time.Duration(-index-1)*time.Hour), now.Add(time.Duration(-index)*time.Hour))
		if err != nil {
			t.Fatalf("queue active job %d: %v", index, err)
		}
	}
	if _, err := service.queueAIJob(AIJobTypeManual, 9, now.Add(-4*time.Hour), now.Add(-3*time.Hour)); !errors.Is(err, ErrAIJobCapacity) {
		t.Fatalf("third active job error = %v, want capacity", err)
	}
	for _, value := range []string{"=cmd()", "+SUM(1,1)", "-1+1", "@test"} {
		if escaped := spreadsheetSafeCell(value); !strings.HasPrefix(escaped, "'") {
			t.Fatalf("spreadsheet cell %q was not escaped: %q", value, escaped)
		}
	}
}

func assertClassificationCount(t *testing.T, findings []AIFinding, classification string, want int) {
	t.Helper()
	got := 0
	for _, finding := range findings {
		if finding.Classification == classification {
			got += finding.Count
		}
	}
	if got != want {
		t.Fatalf("classification %s count = %d, want %d; findings=%#v", classification, got, want, findings)
	}
}
