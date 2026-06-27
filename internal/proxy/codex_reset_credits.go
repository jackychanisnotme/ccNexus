package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/storage"
)

const codexResetCreditTimeout = 30 * time.Second

type CodexResetCredit struct {
	ID         string `json:"id,omitempty"`
	Status     string `json:"status,omitempty"`
	ResetType  string `json:"resetType,omitempty"`
	GrantedAt  *int64 `json:"grantedAt,omitempty"`
	ExpiresAt  *int64 `json:"expiresAt,omitempty"`
	RedeemedAt *int64 `json:"redeemedAt,omitempty"`
	RawStatus  string `json:"rawStatus,omitempty"`
}

type CodexResetCreditsSnapshot struct {
	AvailableCount int64              `json:"availableCount"`
	NextExpiresAt  *int64             `json:"nextExpiresAt,omitempty"`
	Credits        []CodexResetCredit `json:"credits"`
}

type CodexResetCreditConsumeResult struct {
	Consumed     bool   `json:"consumed"`
	CredentialID int64  `json:"credentialId"`
	Message      string `json:"message"`
}

type rawCodexResetCreditsPayload struct {
	Credits             []json.RawMessage `json:"credits"`
	AvailableCount      *int64            `json:"available_count"`
	AvailableCountCamel *int64            `json:"availableCount"`
	NextExpiresAt       *int64            `json:"next_expires_at"`
	NextExpiresAtCamel  *int64            `json:"nextExpiresAt"`
	Data                *struct {
		Credits             []json.RawMessage `json:"credits"`
		AvailableCount      *int64            `json:"available_count"`
		AvailableCountCamel *int64            `json:"availableCount"`
		NextExpiresAt       *int64            `json:"next_expires_at"`
		NextExpiresAtCamel  *int64            `json:"nextExpiresAt"`
	} `json:"data"`
}

type rawCodexResetCredit struct {
	ID              string `json:"id"`
	CreditID        string `json:"credit_id"`
	CreditIDCamel   string `json:"creditId"`
	Status          string `json:"status"`
	State           string `json:"state"`
	Type            string `json:"type"`
	ResetType       string `json:"reset_type"`
	ResetTypeCamel  string `json:"resetType"`
	GrantedAt       *int64 `json:"granted_at"`
	CreatedAt       *int64 `json:"created_at"`
	GrantedAtCamel  *int64 `json:"grantedAt"`
	ExpiresAt       *int64 `json:"expires_at"`
	ExpireAt        *int64 `json:"expire_at"`
	ExpiresAtCamel  *int64 `json:"expiresAt"`
	RedeemedAt      *int64 `json:"redeemed_at"`
	UsedAt          *int64 `json:"used_at"`
	ConsumedAt      *int64 `json:"consumed_at"`
	RedeemedAtCamel *int64 `json:"redeemedAt"`
}

func (p *Proxy) FetchCodexResetCredits(endpoint config.Endpoint, credentialID int64) (*CodexResetCreditsSnapshot, error) {
	cred, err := p.loadCodexResetCreditCredential(endpoint, credentialID)
	if err != nil {
		return nil, err
	}
	if shouldTryCredentialRefresh(cred, time.Now().UTC()) {
		if refreshed, refreshErr := p.refreshCredential(endpoint, cred); refreshErr == nil && refreshed != nil {
			cred = refreshed
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), codexResetCreditTimeout)
	snapshot, status, err := p.fetchCodexResetCreditsOnce(ctx, endpoint, cred)
	cancel()
	if err == nil {
		return snapshot, nil
	}
	if status != "unauthorized" || strings.TrimSpace(cred.RefreshToken) == "" {
		return nil, err
	}

	refreshed, refreshErr := p.refreshCredential(endpoint, cred)
	if refreshErr != nil {
		return nil, refreshErr
	}
	ctx, cancel = context.WithTimeout(context.Background(), codexResetCreditTimeout)
	snapshot, _, err = p.fetchCodexResetCreditsOnce(ctx, endpoint, refreshed)
	cancel()
	return snapshot, err
}

func (p *Proxy) ConsumeCodexResetCredit(endpoint config.Endpoint, credentialID int64) (*CodexResetCreditConsumeResult, error) {
	cred, err := p.loadCodexResetCreditCredential(endpoint, credentialID)
	if err != nil {
		return nil, err
	}
	if shouldTryCredentialRefresh(cred, time.Now().UTC()) {
		if refreshed, refreshErr := p.refreshCredential(endpoint, cred); refreshErr == nil && refreshed != nil {
			cred = refreshed
		}
	}

	redeemRequestID := uuid.NewString()
	ctx, cancel := context.WithTimeout(context.Background(), codexResetCreditTimeout)
	status, err := p.consumeCodexResetCreditOnce(ctx, endpoint, cred, redeemRequestID)
	cancel()
	if err != nil && status == "unauthorized" && strings.TrimSpace(cred.RefreshToken) != "" {
		refreshed, refreshErr := p.refreshCredential(endpoint, cred)
		if refreshErr != nil {
			return nil, refreshErr
		}
		ctx, cancel = context.WithTimeout(context.Background(), codexResetCreditTimeout)
		status, err = p.consumeCodexResetCreditOnce(ctx, endpoint, refreshed, redeemRequestID)
		cancel()
	}
	if err != nil {
		return nil, err
	}
	return &CodexResetCreditConsumeResult{
		Consumed:     true,
		CredentialID: credentialID,
		Message:      "reset credit consumed",
	}, nil
}

func (p *Proxy) loadCodexResetCreditCredential(endpoint config.Endpoint, credentialID int64) (*storage.EndpointCredential, error) {
	if p == nil || p.storage == nil {
		return nil, fmt.Errorf("token storage unavailable")
	}
	if config.NormalizeAuthMode(endpoint.AuthMode) != config.AuthModeCodexTokenPool {
		return nil, fmt.Errorf("codex token pool required")
	}
	if credentialID <= 0 {
		return nil, fmt.Errorf("credential id is required")
	}
	cred, err := p.storage.GetCredentialByID(credentialID)
	if err != nil {
		return nil, fmt.Errorf("failed to load credential: %w", err)
	}
	if cred == nil || cred.EndpointName != endpoint.Name {
		return nil, fmt.Errorf("credential not found")
	}
	if strings.TrimSpace(cred.AccessToken) == "" {
		return nil, fmt.Errorf("access token is empty")
	}
	return cred, nil
}

func (p *Proxy) fetchCodexResetCreditsOnce(ctx context.Context, endpoint config.Endpoint, cred *storage.EndpointCredential) (*CodexResetCreditsSnapshot, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildCodexResetCreditsURL(endpoint, false), nil)
	if err != nil {
		return nil, "error", err
	}
	setCodexResetCreditHeaders(req, cred)

	resp, err := p.codexRateLimitHTTPClient(endpoint).Do(req)
	if err != nil {
		return nil, "network", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "network", err
	}
	if resp.StatusCode != http.StatusOK {
		status := codexResetCreditErrorStatus(resp.StatusCode, body)
		return nil, status, fmt.Errorf("reset credits failed (%d): %s", resp.StatusCode, truncateForLog(string(body), 300))
	}
	snapshot, err := parseCodexResetCreditsSnapshot(body)
	if err != nil {
		return nil, "parse_error", err
	}
	return snapshot, "ok", nil
}

func (p *Proxy) consumeCodexResetCreditOnce(ctx context.Context, endpoint config.Endpoint, cred *storage.EndpointCredential, redeemRequestID string) (string, error) {
	body, err := json.Marshal(map[string]string{"redeem_request_id": redeemRequestID})
	if err != nil {
		return "error", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, buildCodexResetCreditsURL(endpoint, true), bytes.NewReader(body))
	if err != nil {
		return "error", err
	}
	setCodexResetCreditHeaders(req, cred)

	resp, err := p.codexRateLimitHTTPClient(endpoint).Do(req)
	if err != nil {
		return "network", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "network", err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return "ok", nil
	}
	status := codexResetCreditErrorStatus(resp.StatusCode, respBody)
	return status, fmt.Errorf("consume reset credit failed (%d): %s", resp.StatusCode, truncateForLog(string(respBody), 300))
}

func setCodexResetCreditHeaders(req *http.Request, cred *storage.EndpointCredential) {
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cred.AccessToken))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", "https://chatgpt.com/")
	req.Header.Set("User-Agent", config.CodexUserAgent)
	req.Header.Set("OpenAI-Beta", "codex-1")
	req.Header.Set("originator", "Codex Desktop")
	if accountID := strings.TrimSpace(cred.AccountID); accountID != "" {
		req.Header.Set("ChatGPT-Account-Id", accountID)
	}
}

func buildCodexResetCreditsURL(endpoint config.Endpoint, consume bool) string {
	base := strings.TrimSuffix(normalizeCodexRateLimitBaseURL(endpoint.APIUrl), "/")
	suffix := "/wham/rate-limit-reset-credits"
	if consume {
		suffix += "/consume"
	}
	return base + suffix
}

func codexResetCreditErrorStatus(statusCode int, body []byte) string {
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return "unauthorized"
	}
	if isLikelyHTMLResponse(body) {
		return "blocked"
	}
	if statusCode >= 500 {
		return "upstream"
	}
	return "error"
}

func parseCodexResetCreditsSnapshot(body []byte) (*CodexResetCreditsSnapshot, error) {
	var payload rawCodexResetCreditsPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode reset credits failed: %w", err)
	}
	rawCredits := payload.Credits
	availableCount := firstInt64Ptr(payload.AvailableCount, payload.AvailableCountCamel)
	nextExpiresAt := firstInt64Ptr(payload.NextExpiresAt, payload.NextExpiresAtCamel)
	if payload.Data != nil {
		if len(rawCredits) == 0 {
			rawCredits = payload.Data.Credits
		}
		if availableCount == nil {
			availableCount = firstInt64Ptr(payload.Data.AvailableCount, payload.Data.AvailableCountCamel)
		}
		if nextExpiresAt == nil {
			nextExpiresAt = firstInt64Ptr(payload.Data.NextExpiresAt, payload.Data.NextExpiresAtCamel)
		}
	}

	now := time.Now().Unix()
	credits := make([]CodexResetCredit, 0, len(rawCredits))
	computedAvailable := int64(0)
	var computedNextExpiresAt *int64
	for _, raw := range rawCredits {
		credit, ok := parseCodexResetCredit(raw, now)
		if !ok {
			continue
		}
		if codexResetCreditAvailable(credit, now) {
			computedAvailable++
			if credit.ExpiresAt != nil && (computedNextExpiresAt == nil || *credit.ExpiresAt < *computedNextExpiresAt) {
				v := *credit.ExpiresAt
				computedNextExpiresAt = &v
			}
		}
		credits = append(credits, credit)
	}
	if availableCount == nil {
		availableCount = &computedAvailable
	}
	if nextExpiresAt == nil {
		nextExpiresAt = computedNextExpiresAt
	}
	return &CodexResetCreditsSnapshot{
		AvailableCount: *availableCount,
		NextExpiresAt:  nextExpiresAt,
		Credits:        credits,
	}, nil
}

func parseCodexResetCredit(raw json.RawMessage, now int64) (CodexResetCredit, bool) {
	var item rawCodexResetCredit
	if err := json.Unmarshal(raw, &item); err != nil {
		return CodexResetCredit{}, false
	}
	rawStatus := firstString(item.Status, item.State)
	expiresAt := firstInt64Ptr(item.ExpiresAt, item.ExpireAt, item.ExpiresAtCamel)
	status := strings.ToLower(strings.TrimSpace(rawStatus))
	if status == "" && expiresAt != nil && *expiresAt <= now {
		status = "expired"
	}
	return CodexResetCredit{
		ID:         firstString(item.ID, item.CreditID, item.CreditIDCamel),
		Status:     status,
		ResetType:  firstString(item.Type, item.ResetType, item.ResetTypeCamel),
		GrantedAt:  firstInt64Ptr(item.GrantedAt, item.CreatedAt, item.GrantedAtCamel),
		ExpiresAt:  expiresAt,
		RedeemedAt: firstInt64Ptr(item.RedeemedAt, item.UsedAt, item.ConsumedAt, item.RedeemedAtCamel),
		RawStatus:  rawStatus,
	}, true
}

func codexResetCreditAvailable(credit CodexResetCredit, now int64) bool {
	status := strings.ToLower(strings.TrimSpace(firstString(credit.Status, credit.RawStatus)))
	if status == "" {
		status = "available"
	}
	if status == "redeemed" || status == "used" || status == "consumed" || status == "expired" {
		return false
	}
	return credit.ExpiresAt == nil || *credit.ExpiresAt > now
}

func firstString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstInt64Ptr(values ...*int64) *int64 {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
