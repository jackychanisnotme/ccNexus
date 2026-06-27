package service

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/storage"
)

type CodexAccountOverview struct {
	EndpointName                  string         `json:"endpointName"`
	TotalAccounts                 int            `json:"totalAccounts"`
	EnabledAccounts               int            `json:"enabledAccounts"`
	DisabledAccounts              int            `json:"disabledAccounts"`
	ActiveAccounts                int            `json:"activeAccounts"`
	ProblemAccounts               int            `json:"problemAccounts"`
	PlanCounts                    map[string]int `json:"planCounts"`
	HighestPrimaryUsedPercent     int            `json:"highestPrimaryUsedPercent"`
	HighestSecondaryUsedPercent   int            `json:"highestSecondaryUsedPercent"`
	NextResetAt                   *time.Time     `json:"nextResetAt,omitempty"`
	LatestQuotaUpdatedAt          *time.Time     `json:"latestQuotaUpdatedAt,omitempty"`
	Requests                      int            `json:"requests"`
	Errors                        int            `json:"errors"`
	InputTokens                   int            `json:"inputTokens"`
	OutputTokens                  int            `json:"outputTokens"`
	TotalTokens                   int            `json:"totalTokens"`
	QuotaSnapshotAvailableCount   int            `json:"quotaSnapshotAvailableCount"`
	QuotaSnapshotProblemCount     int            `json:"quotaSnapshotProblemCount"`
	QuotaSnapshotUnsupportedCount int            `json:"quotaSnapshotUnsupportedCount"`
}

type codexAccountOverviewStore interface {
	GetEndpointCredentials(endpointName string) ([]storage.EndpointCredential, error)
	GetCredentialRateLimitsByEndpoint(endpointName string) (map[int64]*storage.CredentialRateLimits, error)
	GetCredentialUsageByEndpoint(endpointName string) (map[int64]*storage.CredentialUsage, error)
}

func BuildCodexAccountOverview(endpoint config.Endpoint, store codexAccountOverviewStore) (*CodexAccountOverview, error) {
	if config.NormalizeAuthMode(endpoint.AuthMode) != config.AuthModeCodexTokenPool {
		return nil, fmt.Errorf("Codex Token Pool endpoint required")
	}
	if store == nil {
		return nil, fmt.Errorf("storage unavailable")
	}

	credentials, err := store.GetEndpointCredentials(endpoint.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials: %w", err)
	}
	rateLimits, err := store.GetCredentialRateLimitsByEndpoint(endpoint.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get credential rate limits: %w", err)
	}
	usage, err := store.GetCredentialUsageByEndpoint(endpoint.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get credential usage: %w", err)
	}

	overview := &CodexAccountOverview{
		EndpointName: endpoint.Name,
		PlanCounts:   make(map[string]int),
	}
	for _, credential := range credentials {
		overview.TotalAccounts++
		if credential.Enabled {
			overview.EnabledAccounts++
		} else {
			overview.DisabledAccounts++
		}

		rateLimit := rateLimits[credential.ID]
		if codexOverviewCredentialHealthy(credential, rateLimit) {
			overview.ActiveAccounts++
		} else {
			overview.ProblemAccounts++
		}
		overview.addRateLimit(rateLimit)
		overview.addUsage(usage[credential.ID])
	}
	if overview.TotalAccounts == 0 {
		overview.PlanCounts["unknown"] = 0
	}
	return overview, nil
}

func CodexAccountOverviewJSON(endpoint config.Endpoint, store codexAccountOverviewStore) string {
	overview, err := BuildCodexAccountOverview(endpoint, store)
	if err != nil {
		data, _ := json.Marshal(map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return string(data)
	}
	data, _ := json.Marshal(map[string]any{
		"success": true,
		"data":    overview,
	})
	return string(data)
}

func codexOverviewCredentialHealthy(credential storage.EndpointCredential, rateLimits *storage.CredentialRateLimits) bool {
	if !credential.Enabled {
		return false
	}
	status := strings.TrimSpace(strings.ToLower(credential.Status))
	if status == "" {
		status = "active"
	}
	switch status {
	case "active", "expiring", "need_refresh":
	default:
		return false
	}
	rateStatus := ""
	if rateLimits != nil {
		rateStatus = strings.TrimSpace(strings.ToLower(rateLimits.Status))
	}
	return rateStatus == "" || rateStatus == "ok"
}

func (o *CodexAccountOverview) addRateLimit(rateLimits *storage.CredentialRateLimits) {
	if o == nil {
		return
	}
	if rateLimits == nil {
		o.PlanCounts["unknown"]++
		o.QuotaSnapshotUnsupportedCount++
		return
	}
	if rateLimits.UpdatedAt != nil {
		if o.LatestQuotaUpdatedAt == nil || rateLimits.UpdatedAt.After(*o.LatestQuotaUpdatedAt) {
			updatedAt := rateLimits.UpdatedAt.UTC()
			o.LatestQuotaUpdatedAt = &updatedAt
		}
	}
	rateStatus := strings.TrimSpace(strings.ToLower(rateLimits.Status))
	if rateStatus != "" && rateStatus != "ok" {
		o.QuotaSnapshotProblemCount++
	}
	if rateLimits.Data == nil || rateLimits.Data.Snapshot == nil {
		o.PlanCounts["unknown"]++
		o.QuotaSnapshotUnsupportedCount++
		return
	}
	o.QuotaSnapshotAvailableCount++
	snapshot := rateLimits.Data.Snapshot
	plan := strings.TrimSpace(strings.ToLower(snapshot.PlanType))
	if plan == "" {
		plan = "unknown"
	}
	o.PlanCounts[plan]++
	o.addWindow(snapshot.Primary, true)
	o.addWindow(snapshot.Secondary, false)
}

func (o *CodexAccountOverview) addWindow(window *storage.CodexRateLimitWindow, primary bool) {
	if o == nil || window == nil {
		return
	}
	usedPercent := int(math.Round(window.UsedPercent))
	if primary {
		if usedPercent > o.HighestPrimaryUsedPercent {
			o.HighestPrimaryUsedPercent = usedPercent
		}
	} else if usedPercent > o.HighestSecondaryUsedPercent {
		o.HighestSecondaryUsedPercent = usedPercent
	}
	if window.ResetsAt != nil && *window.ResetsAt > 0 {
		resetAt := time.Unix(*window.ResetsAt, 0).UTC()
		if o.NextResetAt == nil || resetAt.Before(*o.NextResetAt) {
			o.NextResetAt = &resetAt
		}
	}
}

func (o *CodexAccountOverview) addUsage(usage *storage.CredentialUsage) {
	if o == nil || usage == nil {
		return
	}
	o.Requests += usage.Requests
	o.Errors += usage.Errors
	o.InputTokens += usage.InputTokens
	o.OutputTokens += usage.OutputTokens
	o.TotalTokens += usage.InputTokens + usage.OutputTokens
}
