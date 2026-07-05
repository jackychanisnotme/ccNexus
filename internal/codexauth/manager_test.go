package codexauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/storage"
)

type fakeCredentialStore struct {
	mu           sync.Mutex
	nextID       int64
	creds        []storage.EndpointCredential
	saveCalls    int
	updateCalls  int
	beforeGet    func()
	beforeSave   func()
	beforeUpdate func()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newFakeCredentialStore(creds ...storage.EndpointCredential) *fakeCredentialStore {
	s := &fakeCredentialStore{nextID: 1}
	for _, cred := range creds {
		if cred.ID >= s.nextID {
			s.nextID = cred.ID + 1
		}
		s.creds = append(s.creds, cred)
	}
	return s
}

func (s *fakeCredentialStore) GetEndpointCredentials(endpointName string) ([]storage.EndpointCredential, error) {
	if s.beforeGet != nil {
		s.beforeGet()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]storage.EndpointCredential, 0)
	for _, cred := range s.creds {
		if cred.EndpointName == endpointName {
			out = append(out, cred)
		}
	}
	return out, nil
}

func (s *fakeCredentialStore) SaveEndpointCredential(cred *storage.EndpointCredential) error {
	if s.beforeSave != nil {
		s.beforeSave()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cred.ID = s.nextID
	s.nextID++
	s.creds = append(s.creds, *cred)
	s.saveCalls++
	return nil
}

func (s *fakeCredentialStore) UpdateEndpointCredential(cred *storage.EndpointCredential) error {
	if s.beforeUpdate != nil {
		s.beforeUpdate()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.creds {
		if s.creds[i].ID == cred.ID && s.creds[i].EndpointName == cred.EndpointName {
			s.creds[i] = *cred
			s.updateCalls++
			return nil
		}
	}
	return fmt.Errorf("credential not found")
}

func (s *fakeCredentialStore) snapshot() []storage.EndpointCredential {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]storage.EndpointCredential, len(s.creds))
	copy(out, s.creds)
	return out
}

func TestManagerCompletesDeviceCodeLoginAndUpsertsCredential(t *testing.T) {
	existing := storage.EndpointCredential{
		ID:            7,
		EndpointName:  "Codex",
		ProviderType:  "codex",
		AccountID:     "acct-old",
		Email:         "user@example.com",
		AccessToken:   "old-access",
		RefreshToken:  "old-refresh",
		Status:        "invalid",
		Enabled:       false,
		FailureCount:  3,
		CooldownUntil: ptrTime(time.Now().Add(time.Hour)),
		LastError:     "old error",
		Remark:        "keep me",
	}
	store := newFakeCredentialStore(existing)
	idToken := fakeIDToken("acct-new", "user@example.com")
	tokenPolls := 0

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST usercode, got %s", r.Method)
			}
			var req map[string]string
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode usercode request: %v", err)
			}
			if req["client_id"] != ClientID {
				t.Fatalf("expected client_id %q, got %q", ClientID, req["client_id"])
			}
			writeJSON(w, map[string]any{
				"device_auth_id": "device-1",
				"user_code":      "ABCD-1234",
				"interval":       "0",
			})
		case "/api/accounts/deviceauth/token":
			tokenPolls++
			var req map[string]string
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode token poll request: %v", err)
			}
			if req["device_auth_id"] != "device-1" || req["user_code"] != "ABCD-1234" {
				t.Fatalf("unexpected token poll body: %#v", req)
			}
			if tokenPolls == 1 {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			writeJSON(w, map[string]any{
				"authorization_code": "auth-code",
				"code_challenge":     "challenge",
				"code_verifier":      "verifier-secret",
			})
		case "/oauth/token":
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST oauth token, got %s", r.Method)
			}
			raw, _ := ioReadAllString(r)
			values, err := url.ParseQuery(raw)
			if err != nil {
				t.Fatalf("parse token form: %v", err)
			}
			assertFormValue(t, values, "grant_type", "authorization_code")
			assertFormValue(t, values, "code", "auth-code")
			assertFormValue(t, values, "redirect_uri", DeviceCallbackRedirectURI)
			assertFormValue(t, values, "client_id", ClientID)
			assertFormValue(t, values, "code_verifier", "verifier-secret")
			writeJSON(w, map[string]any{
				"access_token":  "new-access",
				"refresh_token": "new-refresh",
				"id_token":      idToken,
				"expires_in":    3600,
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer authServer.Close()

	now := time.Date(2026, 6, 12, 8, 0, 0, 0, time.UTC)
	manager := NewManager(Options{
		Storage:      store,
		HTTPClient:   authServer.Client(),
		Issuer:       authServer.URL,
		Now:          func() time.Time { return now },
		LoginID:      func() string { return "login-1" },
		PollSleep:    func(context.Context, time.Duration) error { return nil },
		PollTimeout:  time.Second,
		SessionTTL:   15 * time.Minute,
		ErrorMaxSize: 500,
	})

	start, err := manager.Start(context.Background(), config.Endpoint{
		Name:     "Codex",
		AuthMode: config.AuthModeCodexTokenPool,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if start.LoginID != "login-1" || start.UserCode != "ABCD-1234" || start.VerificationURL != authServer.URL+"/codex/device" {
		t.Fatalf("unexpected start response: %#v", start)
	}

	status := waitForStatus(t, manager, "login-1", StatusComplete)
	if status.CredentialID != 7 {
		t.Fatalf("expected updated credential id 7, got %d", status.CredentialID)
	}
	if status.Email != "user@example.com" || status.AccountID != "acct-new" {
		t.Fatalf("unexpected account info: %#v", status)
	}

	creds := store.snapshot()
	if len(creds) != 1 {
		t.Fatalf("expected one credential after upsert, got %d", len(creds))
	}
	cred := creds[0]
	if store.saveCalls != 0 || store.updateCalls != 1 {
		t.Fatalf("expected one update and no save, got save=%d update=%d", store.saveCalls, store.updateCalls)
	}
	if cred.AccessToken != "new-access" || cred.RefreshToken != "new-refresh" || cred.IDToken != idToken {
		t.Fatalf("credential tokens were not updated correctly: %#v", cred)
	}
	if cred.ProviderType != "codex" || cred.Status != "active" || !cred.Enabled || cred.FailureCount != 0 || cred.LastError != "" || cred.CooldownUntil != nil {
		t.Fatalf("credential lifecycle fields were not reset: %#v", cred)
	}
	if cred.AccountID != "acct-new" || cred.Email != "user@example.com" || cred.Remark != "keep me" {
		t.Fatalf("credential metadata was not preserved/parsed correctly: %#v", cred)
	}
	if cred.ExpiresAt == nil || !cred.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("expected expires_at one hour from now, got %#v", cred.ExpiresAt)
	}
}

func TestManagerStartParsesNumericInterval(t *testing.T) {
	store := newFakeCredentialStore()
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSON(w, map[string]any{
				"device_auth_id": "device-2",
				"usercode":       "WXYZ-9999",
				"interval":       2,
			})
		case "/api/accounts/deviceauth/token":
			w.WriteHeader(http.StatusForbidden)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer authServer.Close()

	manager := NewManager(Options{
		Storage:     store,
		HTTPClient:  authServer.Client(),
		Issuer:      authServer.URL,
		LoginID:     func() string { return "login-2" },
		PollSleep:   func(context.Context, time.Duration) error { return context.Canceled },
		PollTimeout: time.Second,
		SessionTTL:  15 * time.Minute,
	})

	start, err := manager.Start(context.Background(), config.Endpoint{Name: "Codex", AuthMode: config.AuthModeCodexTokenPool})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if start.PollIntervalSeconds != 2 || start.UserCode != "WXYZ-9999" {
		t.Fatalf("unexpected start response: %#v", start)
	}
}

func TestManagerPollTreatsNotFoundAsPending(t *testing.T) {
	store := newFakeCredentialStore()
	idToken := fakeIDToken("acct-new", "user@example.com")
	var tokenPolls atomic.Int32

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSON(w, map[string]any{
				"device_auth_id": "device-404",
				"user_code":      "NFND-4040",
				"interval":       "0",
			})
		case "/api/accounts/deviceauth/token":
			if tokenPolls.Add(1) == 1 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			writeJSON(w, map[string]any{
				"authorization_code": "auth-code",
				"code_challenge":     "challenge",
				"code_verifier":      "verifier-secret",
			})
		case "/oauth/token":
			writeJSON(w, map[string]any{
				"access_token":  "new-access",
				"refresh_token": "new-refresh",
				"id_token":      idToken,
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer authServer.Close()

	manager := NewManager(Options{
		Storage:     store,
		HTTPClient:  authServer.Client(),
		Issuer:      authServer.URL,
		LoginID:     func() string { return "login-404" },
		PollSleep:   func(context.Context, time.Duration) error { return nil },
		PollTimeout: time.Second,
		SessionTTL:  15 * time.Minute,
	})

	if _, err := manager.Start(context.Background(), config.Endpoint{Name: "Codex", AuthMode: config.AuthModeCodexTokenPool}); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	status := waitForStatus(t, manager, "login-404", StatusComplete)
	if status.Email != "user@example.com" {
		t.Fatalf("unexpected status payload: %#v", status)
	}
	if tokenPolls.Load() != 2 {
		t.Fatalf("expected two poll attempts, got %d", tokenPolls.Load())
	}
}

func TestManagerPollUnexpectedStatusFails(t *testing.T) {
	store := newFakeCredentialStore()
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSON(w, map[string]any{"device_auth_id": "device-429", "user_code": "RATE-4290", "interval": "0"})
		case "/api/accounts/deviceauth/token":
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"slow_down"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer authServer.Close()

	manager := NewManager(Options{
		Storage:     store,
		HTTPClient:  authServer.Client(),
		Issuer:      authServer.URL,
		LoginID:     func() string { return "login-429" },
		PollSleep:   func(context.Context, time.Duration) error { return nil },
		PollTimeout: time.Second,
		SessionTTL:  15 * time.Minute,
	})

	if _, err := manager.Start(context.Background(), config.Endpoint{Name: "Codex", AuthMode: config.AuthModeCodexTokenPool}); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	status := waitForStatus(t, manager, "login-429", StatusFailed)
	if !strings.Contains(status.Error, "429") {
		t.Fatalf("expected poll status error to mention 429, got %q", status.Error)
	}
}

func TestManagerStartReportsDeviceCodeNetworkError(t *testing.T) {
	manager := NewManager(Options{
		Storage: newFakeCredentialStore(),
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("network down")
		})},
	})

	_, err := manager.Start(context.Background(), config.Endpoint{Name: "Codex", AuthMode: config.AuthModeCodexTokenPool})
	if err == nil || !strings.Contains(err.Error(), "device code request failed") || !strings.Contains(err.Error(), "network down") {
		t.Fatalf("expected observable network error, got %v", err)
	}
}

func TestManagerStartReportsUnsupportedOpenAIRegion(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/accounts/deviceauth/usercode" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"unsupported_country_region_territory","message":"Country, region, or territory not supported","param":null,"type":"request_forbidden"}}`))
	}))
	defer authServer.Close()

	manager := NewManager(Options{
		Storage:    newFakeCredentialStore(),
		HTTPClient: authServer.Client(),
		Issuer:     authServer.URL,
	})

	_, err := manager.Start(context.Background(), config.Endpoint{Name: "Codex", AuthMode: config.AuthModeCodexTokenPool})
	if !errors.Is(err, ErrUnsupportedOpenAIRegion) {
		t.Fatalf("expected unsupported region sentinel, got %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "OpenAI rejected this network region") ||
		!strings.Contains(got, "supported network/proxy") ||
		strings.Contains(got, "unsupported_country_region_territory") {
		t.Fatalf("expected normalized actionable error, got %q", got)
	}
}

func TestResolveAuthProxyURLUsesEndpointCodexGlobalPrecedence(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateProxy(&config.ProxyConfig{URL: "http://127.0.0.1:7890"})
	cfg.UpdateCodexProxy(&config.ProxyConfig{URL: "http://127.0.0.1:7891"})

	got := resolveAuthProxyURL(cfg, config.Endpoint{ProxyURL: "http://127.0.0.1:7892"})
	if got != "http://127.0.0.1:7892" {
		t.Fatalf("endpoint proxy should win, got %q", got)
	}

	got = resolveAuthProxyURL(cfg, config.Endpoint{})
	if got != "http://127.0.0.1:7891" {
		t.Fatalf("Codex proxy should win over global proxy, got %q", got)
	}

	cfg.UpdateCodexProxy(nil)
	got = resolveAuthProxyURL(cfg, config.Endpoint{})
	if got != "http://127.0.0.1:7890" {
		t.Fatalf("global proxy should be used when endpoint and Codex proxies are empty, got %q", got)
	}

	cfg.UpdateProxy(nil)
	got = resolveAuthProxyURL(cfg, config.Endpoint{})
	if got != "" {
		t.Fatalf("expected direct connection when no proxy is configured, got %q", got)
	}
}

func TestManagerPollTimeoutFailsSession(t *testing.T) {
	store := newFakeCredentialStore()
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSON(w, map[string]any{"device_auth_id": "device-timeout", "user_code": "TIME-0000", "interval": "0"})
		case "/api/accounts/deviceauth/token":
			w.WriteHeader(http.StatusForbidden)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer authServer.Close()

	manager := NewManager(Options{
		Storage:     store,
		HTTPClient:  authServer.Client(),
		Issuer:      authServer.URL,
		LoginID:     func() string { return "login-timeout" },
		PollSleep:   func(context.Context, time.Duration) error { return nil },
		PollTimeout: time.Nanosecond,
		SessionTTL:  15 * time.Minute,
	})

	if _, err := manager.Start(context.Background(), config.Endpoint{Name: "Codex", AuthMode: config.AuthModeCodexTokenPool}); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	status := waitForStatus(t, manager, "login-timeout", StatusFailed)
	if !strings.Contains(status.Error, "timed out") {
		t.Fatalf("expected timeout error, got %q", status.Error)
	}
}

func TestManagerRejectsNonCodexTokenPoolEndpoint(t *testing.T) {
	manager := NewManager(Options{Storage: newFakeCredentialStore()})

	_, err := manager.Start(context.Background(), config.Endpoint{Name: "OpenAI", AuthMode: config.AuthModeTokenPool})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "codex token pool") {
		t.Fatalf("expected codex token pool error, got %v", err)
	}
}

func TestManagerCancelMarksSessionCanceled(t *testing.T) {
	store := newFakeCredentialStore()
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSON(w, map[string]any{"device_auth_id": "device-3", "user_code": "CCCC-3333", "interval": 1})
		case "/api/accounts/deviceauth/token":
			w.WriteHeader(http.StatusForbidden)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer authServer.Close()

	sleepStarted := make(chan struct{})
	releaseSleep := make(chan struct{})
	manager := NewManager(Options{
		Storage:    store,
		HTTPClient: authServer.Client(),
		Issuer:     authServer.URL,
		LoginID:    func() string { return "login-3" },
		PollSleep: func(ctx context.Context, d time.Duration) error {
			close(sleepStarted)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-releaseSleep:
				return nil
			}
		},
		PollTimeout: time.Minute,
		SessionTTL:  15 * time.Minute,
	})

	if _, err := manager.Start(context.Background(), config.Endpoint{Name: "Codex", AuthMode: config.AuthModeCodexTokenPool}); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	<-sleepStarted
	if err := manager.Cancel("login-3"); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}

	status := waitForStatus(t, manager, "login-3", StatusCanceled)
	if status.Error != "" {
		t.Fatalf("canceled status should not expose an error, got %q", status.Error)
	}
}

func TestManagerTokenExchangeFailureRedactsSensitiveValues(t *testing.T) {
	store := newFakeCredentialStore()
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSON(w, map[string]any{"device_auth_id": "device-4", "user_code": "DDDD-4444", "interval": "0"})
		case "/api/accounts/deviceauth/token":
			writeJSON(w, map[string]any{
				"authorization_code": "auth-code-secret",
				"code_challenge":     "challenge",
				"code_verifier":      "verifier-secret",
			})
		case "/oauth/token":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"bad","access_token":"secret-access","refresh_token":"secret-refresh","id_token":"secret-id"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer authServer.Close()

	manager := NewManager(Options{
		Storage:     store,
		HTTPClient:  authServer.Client(),
		Issuer:      authServer.URL,
		LoginID:     func() string { return "login-4" },
		PollSleep:   func(context.Context, time.Duration) error { return nil },
		PollTimeout: time.Second,
		SessionTTL:  15 * time.Minute,
	})

	if _, err := manager.Start(context.Background(), config.Endpoint{Name: "Codex", AuthMode: config.AuthModeCodexTokenPool}); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	status := waitForStatus(t, manager, "login-4", StatusFailed)
	for _, secret := range []string{"secret-access", "secret-refresh", "secret-id", "verifier-secret", "auth-code-secret"} {
		if strings.Contains(status.Error, secret) {
			t.Fatalf("status error leaked %q: %s", secret, status.Error)
		}
	}
}

func TestSanitizeErrorRedactsClaudeOAuthTokenNames(t *testing.T) {
	message := `{"error":"bad","CLAUDE_CODE_OAUTH_TOKEN":"secret-claude","ANTHROPIC_AUTH_TOKEN":"secret-anthropic","details":"ANTHROPIC_AUTH_TOKEN=\"loose-secret\""}`
	got := sanitizeError(message, 0)
	for _, secret := range []string{"secret-claude", "secret-anthropic", "loose-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("sanitized error leaked %q: %s", secret, got)
		}
	}
}

func TestManagerCancelBeforePersistenceDoesNotSaveCredential(t *testing.T) {
	store := newFakeCredentialStore()
	getStarted := make(chan struct{})
	releaseGet := make(chan struct{})
	saveCalled := make(chan struct{})
	var once sync.Once
	store.beforeGet = func() {
		once.Do(func() { close(getStarted) })
		<-releaseGet
	}
	store.beforeSave = func() {
		close(saveCalled)
	}

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSON(w, map[string]any{"device_auth_id": "device-5", "user_code": "EEEE-5555", "interval": "0"})
		case "/api/accounts/deviceauth/token":
			writeJSON(w, map[string]any{
				"authorization_code": "auth-code",
				"code_challenge":     "challenge",
				"code_verifier":      "verifier-secret",
			})
		case "/oauth/token":
			writeJSON(w, map[string]any{
				"access_token":  "new-access",
				"refresh_token": "new-refresh",
				"id_token":      fakeIDToken("acct-new", "user@example.com"),
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer authServer.Close()

	manager := NewManager(Options{
		Storage:     store,
		HTTPClient:  authServer.Client(),
		Issuer:      authServer.URL,
		LoginID:     func() string { return "login-5" },
		PollSleep:   func(context.Context, time.Duration) error { return nil },
		PollTimeout: time.Second,
		SessionTTL:  15 * time.Minute,
	})

	if _, err := manager.Start(context.Background(), config.Endpoint{Name: "Codex", AuthMode: config.AuthModeCodexTokenPool}); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	select {
	case <-getStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for credential persistence to start")
	}
	if err := manager.Cancel("login-5"); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
	close(releaseGet)

	status := waitForStatus(t, manager, "login-5", StatusCanceled)
	if status.CredentialID != 0 {
		t.Fatalf("canceled login should not expose a credential id, got %d", status.CredentialID)
	}
	select {
	case <-saveCalled:
		t.Fatal("canceled login should not call credential save")
	case <-time.After(200 * time.Millisecond):
	}
	if creds := store.snapshot(); len(creds) != 0 {
		t.Fatalf("canceled login should not save credentials, got %#v", creds)
	}
}

func waitForStatus(t *testing.T, manager *Manager, loginID, want string) StatusResponse {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	var status StatusResponse
	for time.Now().Before(deadline) {
		var err error
		status, err = manager.Status(loginID)
		if err != nil {
			t.Fatalf("Status returned error: %v", err)
		}
		if status.Status == want {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for status %q, last status %#v", want, status)
	return StatusResponse{}
}

func fakeIDToken(accountID, email string) string {
	claims := map[string]any{
		"email": email,
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	}
	payload, _ := json.Marshal(claims)
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func assertFormValue(t *testing.T, values url.Values, key, want string) {
	t.Helper()
	if got := values.Get(key); got != want {
		t.Fatalf("expected form %s=%q, got %q", key, want, got)
	}
}

func ioReadAllString(r *http.Request) (string, error) {
	body, err := io.ReadAll(r.Body)
	return string(body), err
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
