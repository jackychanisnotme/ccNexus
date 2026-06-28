package service

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestBuildCodexAccountOverviewAggregatesCredentialHealthQuotaAndUsage(t *testing.T) {
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	endpoint := config.Endpoint{
		Name:        "Codex Pool",
		AuthMode:    config.AuthModeCodexTokenPool,
		APIUrl:      config.CodexTokenPoolAPIURL,
		Transformer: config.CodexTokenPoolTransformer,
		Enabled:     true,
	}
	first := storage.EndpointCredential{
		EndpointName: endpoint.Name,
		ProviderType: storage.ProviderTypeCodex,
		AccountID:    "acct-1",
		Email:        "first@example.com",
		AccessToken:  "access-1",
		Status:       "active",
		Enabled:      true,
	}
	second := storage.EndpointCredential{
		EndpointName: endpoint.Name,
		ProviderType: storage.ProviderTypeCodex,
		AccountID:    "acct-2",
		Email:        "second@example.com",
		AccessToken:  "access-2",
		Status:       "invalid",
		Enabled:      true,
		LastError:    "secret upstream detail",
	}
	if err := store.SaveEndpointCredential(&first); err != nil {
		t.Fatalf("save first credential: %v", err)
	}
	if err := store.SaveEndpointCredential(&second); err != nil {
		t.Fatalf("save second credential: %v", err)
	}

	primaryWindowMinutes := int64(300)
	secondaryWindowMinutes := int64(10080)
	resetAt := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC).Unix()
	updatedAt := time.Date(2026, 6, 27, 9, 0, 0, 0, time.UTC)
	if err := store.UpsertCredentialRateLimits(first.ID, &storage.CodexRateLimitsData{
		Snapshot: &storage.CodexRateLimitSnapshot{
			PlanType: "plus",
			Primary: &storage.CodexRateLimitWindow{
				UsedPercent:   42.4,
				WindowMinutes: &primaryWindowMinutes,
				ResetsAt:      &resetAt,
			},
			Secondary: &storage.CodexRateLimitWindow{
				UsedPercent:   15.2,
				WindowMinutes: &secondaryWindowMinutes,
			},
		},
		Source: "test",
	}, "ok", "", updatedAt); err != nil {
		t.Fatalf("save first rate limits: %v", err)
	}
	if err := store.UpsertCredentialRateLimits(second.ID, nil, "unauthorized", "token expired", updatedAt.Add(time.Minute)); err != nil {
		t.Fatalf("save second rate limits: %v", err)
	}
	if err := store.UpsertCredentialUsage(first.ID, endpoint.Name, 7, 1, 100, 45, updatedAt); err != nil {
		t.Fatalf("save first usage: %v", err)
	}
	if err := store.UpsertCredentialUsage(second.ID, endpoint.Name, 3, 2, 60, 20, updatedAt); err != nil {
		t.Fatalf("save second usage: %v", err)
	}

	overview, err := BuildCodexAccountOverview(endpoint, store)
	if err != nil {
		t.Fatalf("build overview: %v", err)
	}

	if overview.EndpointName != endpoint.Name || overview.TotalAccounts != 2 || overview.ActiveAccounts != 1 || overview.ProblemAccounts != 1 {
		t.Fatalf("unexpected account counts: %#v", overview)
	}
	if overview.Requests != 10 || overview.Errors != 3 || overview.InputTokens != 160 || overview.OutputTokens != 65 || overview.TotalTokens != 225 {
		t.Fatalf("unexpected usage totals: %#v", overview)
	}
	if overview.HighestPrimaryUsedPercent != 42 || overview.HighestSecondaryUsedPercent != 15 {
		t.Fatalf("unexpected quota maxima: %#v", overview)
	}
	if overview.PlanCounts["plus"] != 1 || overview.PlanCounts["unknown"] != 1 {
		t.Fatalf("unexpected plan counts: %#v", overview.PlanCounts)
	}
	if overview.NextResetAt == nil || overview.LatestQuotaUpdatedAt == nil {
		t.Fatalf("expected reset and quota update timestamps: %#v", overview)
	}
}

func TestCodexAccountOverviewJSONRejectsNonCodexTokenPool(t *testing.T) {
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	raw := CodexAccountOverviewJSON(config.Endpoint{Name: "OpenAI", AuthMode: config.AuthModeAPIKey}, store)
	var response struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatalf("decode response: %v\n%s", err, raw)
	}
	if response.Success {
		t.Fatalf("non-codex endpoint succeeded: %s", raw)
	}
	if response.Error == "" {
		t.Fatalf("expected error in response: %s", raw)
	}
}

func TestBuildCodexTokenPoolHomeSummariesSkipsNonCodexAndMasksSecrets(t *testing.T) {
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	endpoints := []config.Endpoint{
		{Name: "OpenAI", AuthMode: config.AuthModeAPIKey, Enabled: true},
		{Name: "Codex Pool", AuthMode: config.AuthModeCodexTokenPool, Enabled: true},
	}
	lastUsedAt := time.Date(2026, 6, 28, 9, 30, 0, 0, time.UTC)
	codexCredential := storage.EndpointCredential{
		EndpointName:  "Codex Pool",
		ProviderType:  storage.ProviderTypeCodex,
		AccountID:     "account-secret-123456",
		Email:         "codex@example.com",
		AccessToken:   "access-secret",
		RefreshToken:  "refresh-secret",
		IDToken:       "id-secret",
		Status:        "active",
		Enabled:       true,
		LastUsedAt:    &lastUsedAt,
		LastError:     "",
		CooldownUntil: nil,
	}
	if err := store.SaveEndpointCredential(&codexCredential); err != nil {
		t.Fatalf("save codex credential: %v", err)
	}
	disabledCredential := storage.EndpointCredential{
		EndpointName: "Codex Pool",
		ProviderType: storage.ProviderTypeCodex,
		AccountID:    "disabled-account",
		Email:        "disabled@example.com",
		AccessToken:  "disabled-access-secret",
		Status:       "invalid",
		Enabled:      false,
		LastError:    "unauthorized",
	}
	if err := store.SaveEndpointCredential(&disabledCredential); err != nil {
		t.Fatalf("save disabled credential: %v", err)
	}

	primaryReset := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC).Unix()
	secondaryReset := time.Date(2026, 6, 29, 8, 0, 0, 0, time.UTC).Unix()
	quotaUpdatedAt := time.Date(2026, 6, 28, 9, 45, 0, 0, time.UTC)
	if err := store.UpsertCredentialRateLimits(codexCredential.ID, &storage.CodexRateLimitsData{
		Snapshot: &storage.CodexRateLimitSnapshot{
			PlanType: "plus",
			Primary: &storage.CodexRateLimitWindow{
				UsedPercent: 66.6,
				ResetsAt:    &primaryReset,
			},
			Secondary: &storage.CodexRateLimitWindow{
				UsedPercent: 24.2,
				ResetsAt:    &secondaryReset,
			},
		},
		Source: "test",
	}, "ok", "", quotaUpdatedAt); err != nil {
		t.Fatalf("save codex rate limits: %v", err)
	}
	if err := store.UpsertCredentialRateLimits(disabledCredential.ID, nil, "unauthorized", "secret rate limit detail", quotaUpdatedAt.Add(time.Minute)); err != nil {
		t.Fatalf("save disabled rate limits: %v", err)
	}

	summaries, err := BuildCodexTokenPoolHomeSummaries(endpoints, store)
	if err != nil {
		t.Fatalf("build summaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries = %#v, want one Codex pool summary", summaries)
	}
	summary := summaries[0]
	if summary.EndpointName != "Codex Pool" || summary.EndpointIndex != 1 {
		t.Fatalf("unexpected endpoint identity: %#v", summary)
	}
	if summary.TotalAccounts != 2 || summary.ActiveAccounts != 1 || summary.ProblemAccounts != 1 || summary.EnabledAccounts != 1 || summary.DisabledAccounts != 1 {
		t.Fatalf("unexpected account counts: %#v", summary)
	}
	if summary.HighestPrimaryUsedPercent != 67 || summary.HighestSecondaryUsedPercent != 24 {
		t.Fatalf("unexpected quota maxima: %#v", summary)
	}
	if summary.NextResetAt == nil || !summary.NextResetAt.Equal(time.Unix(primaryReset, 0).UTC()) {
		t.Fatalf("unexpected next reset: %#v", summary.NextResetAt)
	}
	if summary.LatestQuotaUpdatedAt == nil || !summary.LatestQuotaUpdatedAt.Equal(quotaUpdatedAt.Add(time.Minute).UTC()) {
		t.Fatalf("unexpected latest quota update: %#v", summary.LatestQuotaUpdatedAt)
	}
	if len(summary.Accounts) != 2 {
		t.Fatalf("accounts = %#v, want two account previews", summary.Accounts)
	}
	if summary.Accounts[0].Label == "" || summary.Accounts[0].AccountID == "account-secret-123456" || summary.Accounts[0].Email == "codex@example.com" {
		t.Fatalf("account preview not label-safe: %#v", summary.Accounts[0])
	}
	if summary.Accounts[0].PrimaryUsedPercent != 67 || summary.Accounts[0].SecondaryUsedPercent != 24 || summary.Accounts[0].RateLimitStatus != "ok" {
		t.Fatalf("unexpected first account quota: %#v", summary.Accounts[0])
	}
	if !summary.Accounts[1].HasError || summary.Accounts[1].ErrorText == "" {
		t.Fatalf("expected compact error on second account: %#v", summary.Accounts[1])
	}

	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	encoded := string(raw)
	for _, secret := range []string{"access-secret", "refresh-secret", "id-secret", "disabled-access-secret", "account-secret-123456", "codex@example.com", "secret upstream detail", "secret rate limit detail"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("summary leaked secret %q in %s", secret, encoded)
		}
	}
}
