package service

import (
	"encoding/json"
	"path/filepath"
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
		LastError:    "unauthorized",
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
