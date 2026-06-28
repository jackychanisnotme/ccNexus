package service

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
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

type CodexTokenPoolHomeAccount struct {
	ID                   int64      `json:"id"`
	Label                string     `json:"label"`
	AccountID            string     `json:"accountId,omitempty"`
	Email                string     `json:"email,omitempty"`
	Enabled              bool       `json:"enabled"`
	Status               string     `json:"status"`
	RateLimitStatus      string     `json:"rateLimitStatus,omitempty"`
	PrimaryUsedPercent   int        `json:"primaryUsedPercent"`
	SecondaryUsedPercent int        `json:"secondaryUsedPercent"`
	ResetAt              *time.Time `json:"resetAt,omitempty"`
	LastUsedAt           *time.Time `json:"lastUsedAt,omitempty"`
	HasError             bool       `json:"hasError"`
	ErrorText            string     `json:"errorText,omitempty"`
}

type CodexTokenPoolHomeSummary struct {
	EndpointName                string                      `json:"endpointName"`
	EndpointIndex               int                         `json:"endpointIndex"`
	TotalAccounts               int                         `json:"totalAccounts"`
	EnabledAccounts             int                         `json:"enabledAccounts"`
	DisabledAccounts            int                         `json:"disabledAccounts"`
	ActiveAccounts              int                         `json:"activeAccounts"`
	ProblemAccounts             int                         `json:"problemAccounts"`
	HighestPrimaryUsedPercent   int                         `json:"highestPrimaryUsedPercent"`
	HighestSecondaryUsedPercent int                         `json:"highestSecondaryUsedPercent"`
	NextResetAt                 *time.Time                  `json:"nextResetAt,omitempty"`
	LatestQuotaUpdatedAt        *time.Time                  `json:"latestQuotaUpdatedAt,omitempty"`
	Accounts                    []CodexTokenPoolHomeAccount `json:"accounts"`
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

func BuildCodexTokenPoolHomeSummaries(endpoints []config.Endpoint, store codexAccountOverviewStore) ([]CodexTokenPoolHomeSummary, error) {
	if store == nil {
		return nil, fmt.Errorf("storage unavailable")
	}
	summaries := make([]CodexTokenPoolHomeSummary, 0)
	for index, endpoint := range endpoints {
		if config.NormalizeAuthMode(endpoint.AuthMode) != config.AuthModeCodexTokenPool {
			continue
		}
		summary, err := buildCodexTokenPoolHomeSummary(endpoint, index, store)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, *summary)
	}
	return summaries, nil
}

func CodexTokenPoolHomeSummariesJSON(endpoints []config.Endpoint, store codexAccountOverviewStore) string {
	summaries, err := BuildCodexTokenPoolHomeSummaries(endpoints, store)
	if err != nil {
		data, _ := json.Marshal(map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return string(data)
	}
	data, _ := json.Marshal(map[string]any{
		"success": true,
		"data":    summaries,
	})
	return string(data)
}

func buildCodexTokenPoolHomeSummary(endpoint config.Endpoint, index int, store codexAccountOverviewStore) (*CodexTokenPoolHomeSummary, error) {
	overview, err := BuildCodexAccountOverview(endpoint, store)
	if err != nil {
		return nil, err
	}
	credentials, err := store.GetEndpointCredentials(endpoint.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials: %w", err)
	}
	rateLimits, err := store.GetCredentialRateLimitsByEndpoint(endpoint.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get credential rate limits: %w", err)
	}

	summary := &CodexTokenPoolHomeSummary{
		EndpointName:                endpoint.Name,
		EndpointIndex:               index,
		TotalAccounts:               overview.TotalAccounts,
		EnabledAccounts:             overview.EnabledAccounts,
		DisabledAccounts:            overview.DisabledAccounts,
		ActiveAccounts:              overview.ActiveAccounts,
		ProblemAccounts:             overview.ProblemAccounts,
		HighestPrimaryUsedPercent:   overview.HighestPrimaryUsedPercent,
		HighestSecondaryUsedPercent: overview.HighestSecondaryUsedPercent,
		NextResetAt:                 overview.NextResetAt,
		LatestQuotaUpdatedAt:        overview.LatestQuotaUpdatedAt,
		Accounts:                    make([]CodexTokenPoolHomeAccount, 0, len(credentials)),
	}

	for _, credential := range credentials {
		summary.Accounts = append(summary.Accounts, buildCodexTokenPoolHomeAccount(credential, rateLimits[credential.ID]))
	}
	sort.SliceStable(summary.Accounts, func(i, j int) bool {
		left := summary.Accounts[i]
		right := summary.Accounts[j]
		if left.LastUsedAt != nil && right.LastUsedAt == nil {
			return true
		}
		if left.LastUsedAt == nil && right.LastUsedAt != nil {
			return false
		}
		if left.LastUsedAt != nil && right.LastUsedAt != nil && !left.LastUsedAt.Equal(*right.LastUsedAt) {
			return left.LastUsedAt.After(*right.LastUsedAt)
		}
		if left.HasError != right.HasError {
			return left.HasError
		}
		return left.Label < right.Label
	})
	return summary, nil
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

func buildCodexTokenPoolHomeAccount(credential storage.EndpointCredential, rateLimits *storage.CredentialRateLimits) CodexTokenPoolHomeAccount {
	accountID := maskCodexHomeAccountID(credential.AccountID)
	email := maskCodexHomeEmail(credential.Email)
	label := email
	if label == "" || label == "-" {
		label = accountID
	}
	if label == "" || label == "-" {
		label = fmt.Sprintf("#%d", credential.ID)
	}
	account := CodexTokenPoolHomeAccount{
		ID:         credential.ID,
		Label:      label,
		AccountID:  accountID,
		Email:      email,
		Enabled:    credential.Enabled,
		Status:     strings.TrimSpace(credential.Status),
		LastUsedAt: cloneTimePtr(credential.LastUsedAt),
	}
	if account.Status == "" {
		account.Status = "active"
	}
	if strings.TrimSpace(credential.LastError) != "" {
		account.HasError = true
		account.ErrorText = "credential_error"
	}
	if rateLimits != nil {
		account.RateLimitStatus = strings.TrimSpace(rateLimits.Status)
		if account.RateLimitStatus != "" && account.RateLimitStatus != "ok" {
			account.HasError = true
			if account.ErrorText == "" {
				account.ErrorText = account.RateLimitStatus
			}
		}
		if rateLimits.Data != nil && rateLimits.Data.Snapshot != nil {
			snapshot := rateLimits.Data.Snapshot
			account.PrimaryUsedPercent = roundCodexHomePercent(snapshot.Primary)
			account.SecondaryUsedPercent = roundCodexHomePercent(snapshot.Secondary)
			account.ResetAt = earliestCodexHomeReset(snapshot.Primary, snapshot.Secondary)
		}
	}
	if account.ErrorText == "" && account.HasError {
		account.ErrorText = account.RateLimitStatus
	}
	return account
}

func maskCodexHomeAccountID(accountID string) string {
	raw := strings.TrimSpace(accountID)
	if raw == "" {
		return "-"
	}
	if len(raw) <= 8 {
		return raw[:1] + "*"
	}
	return raw[:8] + "*"
}

func maskCodexHomeEmail(email string) string {
	raw := strings.TrimSpace(email)
	if raw == "" || !strings.Contains(raw, "@") {
		if raw == "" {
			return "-"
		}
		return maskCodexHomeAccountID(raw)
	}
	parts := strings.SplitN(raw, "@", 2)
	local := parts[0]
	domain := parts[1]
	if local == "" || domain == "" {
		return maskCodexHomeAccountID(raw)
	}
	localMasked := local[:1] + "*"
	if len(local) > 2 {
		localMasked = local[:1] + "*" + local[len(local)-2:]
	}
	domainParts := strings.Split(domain, ".")
	firstLabel := domainParts[0]
	tld := ""
	if len(domainParts) > 1 {
		tld = domainParts[len(domainParts)-1]
	}
	domainMasked := "*"
	if firstLabel != "" {
		domainMasked = firstLabel[:1] + "*"
	}
	if tld != "" {
		domainMasked += tld
	}
	return localMasked + "@" + domainMasked
}

func roundCodexHomePercent(window *storage.CodexRateLimitWindow) int {
	if window == nil {
		return 0
	}
	return int(math.Round(window.UsedPercent))
}

func earliestCodexHomeReset(windows ...*storage.CodexRateLimitWindow) *time.Time {
	var result *time.Time
	for _, window := range windows {
		if window == nil || window.ResetsAt == nil || *window.ResetsAt <= 0 {
			continue
		}
		resetAt := time.Unix(*window.ResetsAt, 0).UTC()
		if result == nil || resetAt.Before(*result) {
			result = &resetAt
		}
	}
	return result
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
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
