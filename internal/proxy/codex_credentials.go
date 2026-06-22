package proxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/storage"
)

const (
	codexOAuthTokenURL  = "https://auth.openai.com/oauth/token"
	codexOAuthClientID  = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexRefreshTimeout = 45 * time.Second
)

var errCodexRefreshTokenReused = errors.New("codex refresh token reused")

type codexRefreshTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type codexRefreshTokenReuseError struct {
	message string
}

func (e *codexRefreshTokenReuseError) Error() string {
	return e.message
}

func (e *codexRefreshTokenReuseError) Unwrap() error {
	return errCodexRefreshTokenReused
}

func shouldTryCredentialRefresh(credential *storage.EndpointCredential, now time.Time) bool {
	if credential == nil {
		return false
	}
	if !isCodexProviderType(credential.ProviderType) {
		return false
	}
	if strings.TrimSpace(credential.RefreshToken) == "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(credential.Status), "need_refresh") {
		return true
	}
	if credential.ExpiresAt == nil {
		return false
	}
	return now.Add(2 * time.Minute).After(credential.ExpiresAt.UTC())
}

func (p *Proxy) refreshCredential(endpoint config.Endpoint, credential *storage.EndpointCredential) (*storage.EndpointCredential, error) {
	if p == nil || p.storage == nil {
		return nil, fmt.Errorf("token storage is unavailable")
	}
	if credential == nil {
		return nil, fmt.Errorf("credential is nil")
	}
	refreshToken := strings.TrimSpace(credential.RefreshToken)
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh token is empty")
	}

	refreshed, err := p.refreshCredentialOnce(endpoint, credential.ID, func() (*storage.EndpointCredential, error) {
		return p.refreshCredentialRequest(endpoint, credential, refreshToken)
	})
	if err != nil {
		if errors.Is(err, errCodexRefreshTokenReused) {
			p.markCredentialReuseInvalid(credential.ID, err.Error())
		}
		return nil, err
	}
	return refreshed, nil
}

func (p *Proxy) markCredentialReuseInvalid(credentialID int64, errMsg string) {
	if p == nil || p.storage == nil || credentialID <= 0 {
		return
	}
	if err := p.storage.MarkCredentialFailure(credentialID, http.StatusUnauthorized, errMsg, time.Now().UTC()); err != nil {
		logger.Warn("Failed to mark reused codex credential invalid (id=%d): %v", credentialID, err)
	}
}

func (p *Proxy) refreshCredentialOnce(endpoint config.Endpoint, credentialID int64, fn func() (*storage.EndpointCredential, error)) (*storage.EndpointCredential, error) {
	if p == nil || credentialID <= 0 {
		return fn()
	}

	call, shared := p.startCredentialRefresh(credentialID)
	if shared {
		<-call.done
		if call.err != nil {
			return nil, call.err
		}
		return call.credential, nil
	}

	call.credential, call.err = fn()
	p.finishCredentialRefresh(credentialID, call)
	return call.credential, call.err
}

func (p *Proxy) refreshCredentialRequest(endpoint config.Endpoint, credential *storage.EndpointCredential, refreshToken string) (*storage.EndpointCredential, error) {
	ctx, cancel := context.WithTimeout(context.Background(), codexRefreshTimeout)
	defer cancel()

	form := url.Values{
		"client_id":     {codexOAuthClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"scope":         {"openid profile email"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexOAuthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create refresh request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.codexRefreshHTTPClient(endpoint).Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request failed (%s): %w", codexOAuthTokenURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read refresh response failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if isCodexRefreshTokenReusedResponse(resp.StatusCode, body) {
			return nil, &codexRefreshTokenReuseError{
				message: fmt.Sprintf("refresh token reused: please sign in again (%d): %s", resp.StatusCode, truncateForLog(string(body), 2000)),
			}
		}
		return nil, fmt.Errorf("refresh failed (%d): %s", resp.StatusCode, truncateForLog(string(body), 2000))
	}

	var tokenResp codexRefreshTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse refresh response failed: %w", err)
	}
	tokenResp.AccessToken = strings.TrimSpace(tokenResp.AccessToken)
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("refresh response missing access_token")
	}

	now := time.Now().UTC()
	updated := *credential
	updated.AccessToken = tokenResp.AccessToken
	if refresh := strings.TrimSpace(tokenResp.RefreshToken); refresh != "" {
		updated.RefreshToken = refresh
	}
	if idToken := strings.TrimSpace(tokenResp.IDToken); idToken != "" {
		updated.IDToken = idToken
		accountID, email := parseCodexIDToken(idToken)
		if accountID != "" {
			updated.AccountID = accountID
		}
		if email != "" {
			updated.Email = email
		}
	}
	if tokenResp.ExpiresIn > 0 {
		expiresAt := now.Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		updated.ExpiresAt = &expiresAt
	}
	updated.LastRefresh = &now
	updated.Status = "active"
	updated.FailureCount = 0
	updated.CooldownUntil = nil
	updated.LastError = ""
	updated.LastCheckedAt = &now

	if err := p.storage.UpdateEndpointCredential(&updated); err != nil {
		return nil, fmt.Errorf("persist refreshed credential failed: %w", err)
	}
	logger.Info("[%s] Refreshed token pool credential id=%d", endpoint.Name, credential.ID)
	return &updated, nil
}

func isCodexRefreshTokenReusedResponse(statusCode int, body []byte) bool {
	if statusCode != http.StatusUnauthorized && statusCode != http.StatusForbidden {
		return false
	}
	lowered := strings.ToLower(string(body))
	return strings.Contains(lowered, "refresh_token_reused") ||
		strings.Contains(lowered, "has already been used to generate a new access token") ||
		strings.Contains(lowered, "please try signing in again")
}

func (p *Proxy) RefreshCodexCredential(endpoint config.Endpoint, credentialID int64) (*storage.EndpointCredential, error) {
	if p == nil || p.storage == nil {
		return nil, fmt.Errorf("token storage is unavailable")
	}
	if credentialID <= 0 {
		return nil, fmt.Errorf("credential id is required")
	}
	if config.NormalizeAuthMode(endpoint.AuthMode) != config.AuthModeCodexTokenPool {
		return nil, fmt.Errorf("codex token pool required")
	}

	cred, err := p.storage.GetCredentialByID(credentialID)
	if err != nil {
		return nil, fmt.Errorf("failed to load credential: %w", err)
	}
	if cred == nil || cred.EndpointName != endpoint.Name {
		return nil, fmt.Errorf("credential not found")
	}

		refreshed, err := p.refreshCredential(endpoint, cred)
	if err != nil {
		if !errors.Is(err, errCodexRefreshTokenReused) {
			p.markCredentialFailure(credentialID, 0, err.Error())
		}
		if errors.Is(err, errCodexRefreshTokenReused) {
			return nil, fmt.Errorf("%w: please sign in again", err)
		}
		return nil, err
	}
	return refreshed, nil
}

func (p *Proxy) codexRefreshHTTPClient(endpoint config.Endpoint) *http.Client {
	client := &http.Client{Timeout: codexRefreshTimeout}
	if p != nil && p.httpClient != nil {
		client.Transport = p.httpClient.Transport
	}
	if p == nil || p.config == nil {
		return client
	}

	proxyURL := config.ResolveEndpointProxyURL(&endpoint, config.CodexTokenPoolAPIURL, p.config.GetProxy(), p.config.GetCodexProxy())
	if strings.TrimSpace(proxyURL) == "" {
		return client
	}
	transport, err := CreateProxyTransport(proxyURL)
	if err != nil {
		logger.Warn("Failed to create proxy transport for credential refresh: %v", err)
		return client
	}
	client.Transport = transport
	logger.Debug("Using proxy for credential refresh: %s", proxyURL)
	return client
}

func parseCodexIDToken(token string) (accountID, email string) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return "", ""
	}
	payload, err := decodeJWTPart(parts[1])
	if err != nil {
		return "", ""
	}

	var claims struct {
		Email string `json:"email"`
		Auth  struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", ""
	}
	return strings.TrimSpace(claims.Auth.ChatGPTAccountID), strings.TrimSpace(claims.Email)
}

func decodeJWTPart(raw string) ([]byte, error) {
	if payload, err := base64.RawURLEncoding.DecodeString(raw); err == nil {
		return payload, nil
	}
	switch len(raw) % 4 {
	case 2:
		raw += "=="
	case 3:
		raw += "="
	}
	return base64.URLEncoding.DecodeString(raw)
}

func truncateForLog(message string, max int) string {
	message = strings.TrimSpace(message)
	if max <= 0 || len(message) <= max {
		return message
	}
	return message[:max] + "..."
}
