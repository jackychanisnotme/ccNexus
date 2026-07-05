package codexauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/storage"
)

const (
	ClientID                  = "app_EMoamEEZ73f0CkXaXp7hrann"
	DefaultIssuer             = "https://auth.openai.com"
	DeviceCallbackRedirectURI = "https://auth.openai.com/deviceauth/callback"

	StatusPending  = "pending"
	StatusComplete = "complete"
	StatusFailed   = "failed"
	StatusCanceled = "canceled"
	StatusExpired  = "expired"
)

var (
	errSessionNotPending = errors.New("login session is no longer pending")
	// ErrUnsupportedOpenAIRegion marks OpenAI device-auth responses blocked by request region.
	ErrUnsupportedOpenAIRegion = errors.New("OpenAI rejected this network region. Configure a supported network/proxy for this Codex Token Pool endpoint, then retry.")
)

const (
	unsupportedOpenAIRegionCode = "unsupported_country_region_territory"
	defaultSessionTTL           = 15 * time.Minute
	defaultPollTimeout          = 15 * time.Minute
	defaultHTTPTimeout          = 45 * time.Second
	defaultErrorMaxSize         = 1000
)

type CredentialStore interface {
	GetEndpointCredentials(endpointName string) ([]storage.EndpointCredential, error)
	SaveEndpointCredential(cred *storage.EndpointCredential) error
	UpdateEndpointCredential(cred *storage.EndpointCredential) error
}

type Options struct {
	Storage               CredentialStore
	HTTPClient            *http.Client
	HTTPClientForEndpoint func(config.Endpoint) *http.Client
	Issuer                string
	Now                   func() time.Time
	LoginID               func() string
	PollSleep             func(context.Context, time.Duration) error
	PollTimeout           time.Duration
	SessionTTL            time.Duration
	ErrorMaxSize          int
}

type Manager struct {
	storage               CredentialStore
	httpClient            *http.Client
	httpClientForEndpoint func(config.Endpoint) *http.Client
	issuer                string
	now                   func() time.Time
	loginID               func() string
	pollSleep             func(context.Context, time.Duration) error
	pollTimeout           time.Duration
	sessionTTL            time.Duration
	errorMax              int

	mu       sync.RWMutex
	sessions map[string]*session
}

type session struct {
	ID           string
	EndpointName string
	Status       string
	ExpiresAt    time.Time
	Error        string
	CredentialID int64
	AccountID    string
	Email        string
	cancel       context.CancelFunc
}

type StartResponse struct {
	LoginID             string    `json:"loginId"`
	VerificationURL     string    `json:"verificationUrl"`
	UserCode            string    `json:"userCode"`
	ExpiresAt           time.Time `json:"expiresAt"`
	PollIntervalSeconds int64     `json:"pollIntervalSeconds"`
}

type StatusResponse struct {
	LoginID      string    `json:"loginId"`
	Status       string    `json:"status"`
	ExpiresAt    time.Time `json:"expiresAt"`
	CredentialID int64     `json:"credentialId,omitempty"`
	AccountID    string    `json:"accountId,omitempty"`
	Email        string    `json:"email,omitempty"`
	Error        string    `json:"error,omitempty"`
}

type deviceCode struct {
	VerificationURL string
	UserCode        string
	DeviceAuthID    string
	Interval        int64
}

type authCodeResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeChallenge     string `json:"code_challenge"`
	CodeVerifier      string `json:"code_verifier"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func NewManager(opts Options) *Manager {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	issuer := strings.TrimRight(strings.TrimSpace(opts.Issuer), "/")
	if issuer == "" {
		issuer = DefaultIssuer
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	loginID := opts.LoginID
	if loginID == nil {
		loginID = func() string { return uuid.NewString() }
	}
	pollSleep := opts.PollSleep
	if pollSleep == nil {
		pollSleep = sleepContext
	}
	pollTimeout := opts.PollTimeout
	if pollTimeout <= 0 {
		pollTimeout = defaultPollTimeout
	}
	sessionTTL := opts.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = defaultSessionTTL
	}
	errorMax := opts.ErrorMaxSize
	if errorMax <= 0 {
		errorMax = defaultErrorMaxSize
	}

	return &Manager{
		storage:               opts.Storage,
		httpClient:            client,
		httpClientForEndpoint: opts.HTTPClientForEndpoint,
		issuer:                issuer,
		now:                   now,
		loginID:               loginID,
		pollSleep:             pollSleep,
		pollTimeout:           pollTimeout,
		sessionTTL:            sessionTTL,
		errorMax:              errorMax,
		sessions:              make(map[string]*session),
	}
}

func (m *Manager) clientForEndpoint(endpoint config.Endpoint) *http.Client {
	if m != nil && m.httpClientForEndpoint != nil {
		if client := m.httpClientForEndpoint(endpoint); client != nil {
			return client
		}
	}
	if m != nil && m.httpClient != nil {
		return m.httpClient
	}
	return &http.Client{Timeout: defaultHTTPTimeout}
}

func (m *Manager) Start(ctx context.Context, endpoint config.Endpoint) (StartResponse, error) {
	if m == nil || m.storage == nil {
		return StartResponse{}, fmt.Errorf("codex auth storage unavailable")
	}
	if config.NormalizeAuthMode(endpoint.AuthMode) != config.AuthModeCodexTokenPool {
		return StartResponse{}, fmt.Errorf("codex token pool endpoint required")
	}
	if strings.TrimSpace(endpoint.Name) == "" {
		return StartResponse{}, fmt.Errorf("endpoint name is required")
	}

	code, err := m.requestDeviceCode(ctx, endpoint)
	if err != nil {
		return StartResponse{}, err
	}

	now := m.now().UTC()
	loginID := m.loginID()
	expiresAt := now.Add(m.sessionTTL)
	sessionCtx, cancel := context.WithTimeout(context.Background(), m.sessionTTL)
	sess := &session{
		ID:           loginID,
		EndpointName: endpoint.Name,
		Status:       StatusPending,
		ExpiresAt:    expiresAt,
		cancel:       cancel,
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[loginID] = sess

	go m.completeLogin(sessionCtx, loginID, endpoint, code)

	return StartResponse{
		LoginID:             loginID,
		VerificationURL:     code.VerificationURL,
		UserCode:            code.UserCode,
		ExpiresAt:           expiresAt,
		PollIntervalSeconds: code.Interval,
	}, nil
}

func (m *Manager) Status(loginID string) (StatusResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sess, ok := m.sessions[strings.TrimSpace(loginID)]
	if !ok {
		return StatusResponse{}, fmt.Errorf("login session not found")
	}

	status := sess.Status
	if status == StatusPending && !m.now().UTC().Before(sess.ExpiresAt) {
		status = StatusExpired
	}
	return StatusResponse{
		LoginID:      sess.ID,
		Status:       status,
		ExpiresAt:    sess.ExpiresAt,
		CredentialID: sess.CredentialID,
		AccountID:    sess.AccountID,
		Email:        sess.Email,
		Error:        sess.Error,
	}, nil
}

func (m *Manager) Cancel(loginID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[strings.TrimSpace(loginID)]
	if !ok {
		return fmt.Errorf("login session not found")
	}
	if sess.Status == StatusPending {
		sess.Status = StatusCanceled
		sess.Error = ""
		if sess.cancel != nil {
			sess.cancel()
		}
	}
	return nil
}

func (m *Manager) completeLogin(ctx context.Context, loginID string, endpoint config.Endpoint, code deviceCode) {
	authCode, err := m.pollForAuthorizationCode(ctx, endpoint, code)
	if err != nil {
		m.failSession(loginID, ctx, err)
		return
	}

	tokens, err := m.exchangeCodeForTokens(ctx, endpoint, authCode)
	if err != nil {
		m.failSession(loginID, ctx, err)
		return
	}

	cred, err := m.upsertCredential(ctx, loginID, endpoint.Name, tokens)
	if errors.Is(err, errSessionNotPending) {
		return
	}
	if err != nil {
		m.failSession(loginID, ctx, err)
		return
	}
	if cred == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[loginID]; ok && sess.Status == StatusPending {
		sess.Status = StatusComplete
		sess.CredentialID = cred.ID
		sess.AccountID = cred.AccountID
		sess.Email = cred.Email
		sess.Error = ""
		if sess.cancel != nil {
			sess.cancel()
		}
	}
}

func (m *Manager) failSession(loginID string, ctx context.Context, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[loginID]
	if !ok || sess.Status != StatusPending {
		return
	}
	if ctx != nil && ctx.Err() != nil {
		sess.Status = StatusExpired
		sess.Error = sanitizeError(ctx.Err().Error(), m.errorMax)
		return
	}
	sess.Status = StatusFailed
	sess.Error = sanitizeError(err.Error(), m.errorMax)
}

func (m *Manager) requestDeviceCode(ctx context.Context, endpoint config.Endpoint) (deviceCode, error) {
	body, err := json.Marshal(map[string]string{"client_id": ClientID})
	if err != nil {
		return deviceCode{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.issuer+"/api/accounts/deviceauth/usercode", bytes.NewReader(body))
	if err != nil {
		return deviceCode{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := m.clientForEndpoint(endpoint).Do(req)
	if err != nil {
		return deviceCode{}, fmt.Errorf("device code request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return deviceCode{}, fmt.Errorf("read device code response failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return deviceCode{}, deviceCodeRequestError(resp.StatusCode, raw, m.errorMax)
	}

	var payload userCodePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return deviceCode{}, fmt.Errorf("parse device code response failed: %w", err)
	}
	if strings.TrimSpace(payload.DeviceAuthID) == "" || strings.TrimSpace(payload.UserCode) == "" {
		return deviceCode{}, fmt.Errorf("device code response missing device_auth_id or user_code")
	}
	return deviceCode{
		VerificationURL: m.issuer + "/codex/device",
		UserCode:        strings.TrimSpace(payload.UserCode),
		DeviceAuthID:    strings.TrimSpace(payload.DeviceAuthID),
		Interval:        payload.Interval,
	}, nil
}

func deviceCodeRequestError(statusCode int, raw []byte, errorMax int) error {
	if isUnsupportedOpenAIRegionResponse(raw) {
		return fmt.Errorf("device code request failed: %w", ErrUnsupportedOpenAIRegion)
	}
	return fmt.Errorf("device code request failed (%d): %s", statusCode, sanitizeError(string(raw), errorMax))
}

func isUnsupportedOpenAIRegionResponse(raw []byte) bool {
	var payload struct {
		Code  string `json:"code"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(payload.Code), unsupportedOpenAIRegionCode) ||
		strings.EqualFold(strings.TrimSpace(payload.Error.Code), unsupportedOpenAIRegionCode)
}

type userCodePayload struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string
	Interval     int64
}

func (p *userCodePayload) UnmarshalJSON(data []byte) error {
	var raw struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		Usercode     string          `json:"usercode"`
		Interval     json.RawMessage `json:"interval"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.DeviceAuthID = raw.DeviceAuthID
	p.UserCode = raw.UserCode
	if p.UserCode == "" {
		p.UserCode = raw.Usercode
	}
	if len(raw.Interval) == 0 || string(raw.Interval) == "null" {
		p.Interval = 5
		return nil
	}
	var n int64
	if err := json.Unmarshal(raw.Interval, &n); err == nil {
		p.Interval = n
		return nil
	}
	var s string
	if err := json.Unmarshal(raw.Interval, &s); err != nil {
		return err
	}
	parsed, err := parseInt64(strings.TrimSpace(s))
	if err != nil {
		return err
	}
	p.Interval = parsed
	return nil
}

func (m *Manager) pollForAuthorizationCode(ctx context.Context, endpoint config.Endpoint, code deviceCode) (authCodeResponse, error) {
	deadline := time.Now().Add(m.pollTimeout)
	interval := time.Duration(code.Interval) * time.Second
	if interval < 0 {
		interval = 0
	}

	for {
		authCode, pending, err := m.pollOnce(ctx, endpoint, code)
		if err == nil && !pending {
			return authCode, nil
		}
		if err != nil {
			return authCodeResponse{}, err
		}
		if !time.Now().Before(deadline) {
			return authCodeResponse{}, fmt.Errorf("device auth timed out after %s", m.pollTimeout)
		}
		if err := m.pollSleep(ctx, interval); err != nil {
			return authCodeResponse{}, err
		}
	}
}

func (m *Manager) pollOnce(ctx context.Context, endpoint config.Endpoint, code deviceCode) (authCodeResponse, bool, error) {
	body, err := json.Marshal(map[string]string{
		"device_auth_id": code.DeviceAuthID,
		"user_code":      code.UserCode,
	})
	if err != nil {
		return authCodeResponse{}, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.issuer+"/api/accounts/deviceauth/token", bytes.NewReader(body))
	if err != nil {
		return authCodeResponse{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := m.clientForEndpoint(endpoint).Do(req)
	if err != nil {
		return authCodeResponse{}, false, fmt.Errorf("device auth poll failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return authCodeResponse{}, false, fmt.Errorf("read device auth poll response failed: %w", err)
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return authCodeResponse{}, true, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return authCodeResponse{}, false, fmt.Errorf("device auth failed (%d): %s", resp.StatusCode, sanitizeError(string(raw), m.errorMax))
	}
	var authCode authCodeResponse
	if err := json.Unmarshal(raw, &authCode); err != nil {
		return authCodeResponse{}, false, fmt.Errorf("parse device auth response failed: %w", err)
	}
	if strings.TrimSpace(authCode.AuthorizationCode) == "" || strings.TrimSpace(authCode.CodeVerifier) == "" {
		return authCodeResponse{}, false, fmt.Errorf("device auth response missing authorization_code or code_verifier")
	}
	return authCode, false, nil
}

func (m *Manager) exchangeCodeForTokens(ctx context.Context, endpoint config.Endpoint, authCode authCodeResponse) (tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", strings.TrimSpace(authCode.AuthorizationCode))
	form.Set("redirect_uri", DeviceCallbackRedirectURI)
	form.Set("client_id", ClientID)
	form.Set("code_verifier", strings.TrimSpace(authCode.CodeVerifier))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.issuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := m.clientForEndpoint(endpoint).Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("read token exchange response failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenResponse{}, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, sanitizeError(string(raw), m.errorMax))
	}
	var tokens tokenResponse
	if err := json.Unmarshal(raw, &tokens); err != nil {
		return tokenResponse{}, fmt.Errorf("parse token exchange response failed: %w", err)
	}
	tokens.AccessToken = strings.TrimSpace(tokens.AccessToken)
	if tokens.AccessToken == "" {
		return tokenResponse{}, fmt.Errorf("token exchange response missing access_token")
	}
	tokens.RefreshToken = strings.TrimSpace(tokens.RefreshToken)
	tokens.IDToken = strings.TrimSpace(tokens.IDToken)
	return tokens, nil
}

func (m *Manager) upsertCredential(ctx context.Context, loginID, endpointName string, tokens tokenResponse) (*storage.EndpointCredential, error) {
	if err := m.ensureSessionPending(ctx, loginID); err != nil {
		return nil, err
	}

	accountID, email := parseIDToken(tokens.IDToken)
	now := m.now().UTC()
	cred := storage.EndpointCredential{
		EndpointName:  endpointName,
		ProviderType:  "codex",
		AccountID:     accountID,
		Email:         email,
		AccessToken:   tokens.AccessToken,
		RefreshToken:  tokens.RefreshToken,
		IDToken:       tokens.IDToken,
		LastRefresh:   &now,
		Status:        "active",
		Enabled:       true,
		FailureCount:  0,
		CooldownUntil: nil,
		LastCheckedAt: &now,
		LastError:     "",
		Remark:        "ChatGPT Auth",
	}
	if tokens.ExpiresIn > 0 {
		expiresAt := now.Add(time.Duration(tokens.ExpiresIn) * time.Second)
		cred.ExpiresAt = &expiresAt
	}

	existing, err := m.storage.GetEndpointCredentials(endpointName)
	if err != nil {
		return nil, fmt.Errorf("load existing credentials failed: %w", err)
	}
	if err := m.ensureSessionPending(ctx, loginID); err != nil {
		return nil, err
	}
	if match := findExisting(existing, accountID, email); match != nil {
		cred.ID = match.ID
		if strings.TrimSpace(match.Remark) != "" {
			cred.Remark = match.Remark
		}
		if err := m.ensureSessionPending(ctx, loginID); err != nil {
			return nil, err
		}
		if err := m.storage.UpdateEndpointCredential(&cred); err != nil {
			return nil, fmt.Errorf("update credential failed: %w", err)
		}
		return &cred, nil
	}
	if err := m.ensureSessionPending(ctx, loginID); err != nil {
		return nil, err
	}
	if err := m.storage.SaveEndpointCredential(&cred); err != nil {
		return nil, fmt.Errorf("save credential failed: %w", err)
	}
	return &cred, nil
}

func (m *Manager) ensureSessionPending(ctx context.Context, loginID string) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.sessions[loginID]
	if !ok || sess.Status != StatusPending || !m.now().UTC().Before(sess.ExpiresAt) {
		return errSessionNotPending
	}
	return nil
}

func findExisting(credentials []storage.EndpointCredential, accountID, email string) *storage.EndpointCredential {
	accountID = strings.TrimSpace(accountID)
	email = strings.TrimSpace(email)
	if accountID != "" {
		for i := range credentials {
			if strings.TrimSpace(credentials[i].AccountID) == accountID {
				return &credentials[i]
			}
		}
	}
	if email != "" {
		for i := range credentials {
			if strings.EqualFold(strings.TrimSpace(credentials[i].Email), email) {
				return &credentials[i]
			}
		}
	}
	return nil
}

func parseIDToken(token string) (accountID, email string) {
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

func sanitizeError(message string, max int) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	var payload interface{}
	if err := json.Unmarshal([]byte(message), &payload); err == nil {
		redactJSON(payload)
		if redacted, err := json.Marshal(payload); err == nil {
			message = string(redacted)
		}
	}
	for _, key := range secretFieldNames() {
		message = redactLooseKey(message, key)
	}
	if max > 0 && len(message) > max {
		message = message[:max] + "..."
	}
	return message
}

func redactJSON(value interface{}) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if isSecretFieldName(key) {
				typed[key] = "<redacted>"
				continue
			}
			redactJSON(child)
		}
	case []interface{}:
		for _, child := range typed {
			redactJSON(child)
		}
	}
}

func secretFieldNames() []string {
	return []string{
		"access_token",
		"refresh_token",
		"id_token",
		"authorization_code",
		"code_verifier",
		"ANTHROPIC_AUTH_TOKEN",
		"CLAUDE_CODE_OAUTH_TOKEN",
	}
}

func isSecretFieldName(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, secretKey := range secretFieldNames() {
		if normalized == strings.ToLower(secretKey) {
			return true
		}
	}
	return false
}

func redactLooseKey(message, key string) string {
	normalizedKey := strings.ToLower(strings.TrimSpace(key))
	searchStart := 0
	for searchStart < len(message) {
		idx := strings.Index(strings.ToLower(message[searchStart:]), normalizedKey)
		if idx < 0 {
			break
		}
		keyStart := searchStart + idx
		afterKey := keyStart + len(key)
		separator := afterKey
		for separator < len(message) && isLooseSecretJunk(message[separator]) {
			separator++
		}
		if separator >= len(message) || (message[separator] != ':' && message[separator] != '=') {
			searchStart = afterKey
			continue
		}
		valueStart := separator + 1
		for valueStart < len(message) && isLooseSecretJunk(message[valueStart]) {
			valueStart++
		}
		valueEnd := valueStart
		for valueEnd < len(message) && !isLooseSecretTerminator(message[valueEnd]) {
			valueEnd++
		}
		if valueEnd > valueStart {
			message = message[:valueStart] + "<redacted>" + message[valueEnd:]
			searchStart = valueStart + len("<redacted>")
		} else {
			searchStart = afterKey
		}
	}
	return message
}

func isLooseSecretJunk(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == '"' || ch == '\'' || ch == '\\'
}

func isLooseSecretTerminator(ch byte) bool {
	return ch == '"' || ch == '\'' || ch == '\\' || ch == ',' || ch == '}' || ch == ']' || ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n'
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseInt64(value string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(value, "%d", &n)
	return n, err
}
