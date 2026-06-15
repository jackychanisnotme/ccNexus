package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/codexauth"
	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/storage"
)

type stubCodexAuthManager struct {
	startEndpoint config.Endpoint
	startResponse codexauth.StartResponse
	startErr      error
	statusID      string
	statusResp    codexauth.StatusResponse
	statusErr     error
	cancelID      string
	cancelErr     error
}

func (s *stubCodexAuthManager) Start(ctx context.Context, endpoint config.Endpoint) (codexauth.StartResponse, error) {
	s.startEndpoint = endpoint
	return s.startResponse, s.startErr
}

func (s *stubCodexAuthManager) Status(loginID string) (codexauth.StatusResponse, error) {
	s.statusID = loginID
	return s.statusResp, s.statusErr
}

func (s *stubCodexAuthManager) Cancel(loginID string) error {
	s.cancelID = loginID
	return s.cancelErr
}

func TestCredentialAuthRoutesStartStatusCancel(t *testing.T) {
	store := newAPITestStorage(t)
	saveAPITestEndpoint(t, store, storage.Endpoint{
		Name:        "Codex",
		APIUrl:      config.CodexTokenPoolAPIURL,
		AuthMode:    config.AuthModeCodexTokenPool,
		Transformer: config.CodexTokenPoolTransformer,
		Enabled:     true,
	})

	manager := &stubCodexAuthManager{
		startResponse: codexauth.StartResponse{
			LoginID:             "login-1",
			VerificationURL:     "https://auth.openai.com/codex/device",
			UserCode:            "ABCD-1234",
			ExpiresAt:           time.Date(2026, 6, 12, 8, 15, 0, 0, time.UTC),
			PollIntervalSeconds: 5,
		},
		statusResp: codexauth.StatusResponse{
			LoginID:      "login-1",
			Status:       codexauth.StatusComplete,
			CredentialID: 42,
			AccountID:    "acct",
			Email:        "user@example.com",
		},
	}
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	handler := NewHandler(cfg, nil, store)
	handler.codexAuth = manager

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/endpoints/Codex/credentials/auth/start", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", rec.Code, rec.Body.String())
	}
	var startResp SuccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	data := startResp.Data.(map[string]interface{})
	if data["loginId"] != "login-1" || data["userCode"] != "ABCD-1234" {
		t.Fatalf("unexpected start payload: %#v", data)
	}
	if manager.startEndpoint.Name != "Codex" || manager.startEndpoint.AuthMode != config.AuthModeCodexTokenPool {
		t.Fatalf("unexpected endpoint passed to auth manager: %#v", manager.startEndpoint)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/endpoints/Codex/credentials/auth/login-1", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code=%d body=%s", rec.Code, rec.Body.String())
	}
	if manager.statusID != "login-1" {
		t.Fatalf("expected status login id login-1, got %q", manager.statusID)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/endpoints/Codex/credentials/auth/login-1", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel code=%d body=%s", rec.Code, rec.Body.String())
	}
	if manager.cancelID != "login-1" {
		t.Fatalf("expected cancel login id login-1, got %q", manager.cancelID)
	}
}

func TestCredentialAuthRouteRejectsNonCodexTokenPoolEndpoint(t *testing.T) {
	store := newAPITestStorage(t)
	saveAPITestEndpoint(t, store, storage.Endpoint{
		Name:        "Generic",
		APIUrl:      "https://api.example.com",
		AuthMode:    config.AuthModeTokenPool,
		Transformer: "openai2",
		Enabled:     true,
	})

	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	handler := NewHandler(cfg, nil, store)
	handler.codexAuth = &stubCodexAuthManager{}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/endpoints/Generic/credentials/auth/start", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "codex token pool") {
		t.Fatalf("expected codex token pool error, got %s", rec.Body.String())
	}
}

func TestCredentialAuthRoutesRequireBasicAuth(t *testing.T) {
	store := newAPITestStorage(t)
	saveAPITestEndpoint(t, store, storage.Endpoint{
		Name:        "Codex",
		APIUrl:      config.CodexTokenPoolAPIURL,
		AuthMode:    config.AuthModeCodexTokenPool,
		Transformer: config.CodexTokenPoolTransformer,
		Enabled:     true,
	})
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = true
	cfg.BasicAuthUsername = "admin"
	cfg.BasicAuthPassword = "secret"

	handler := NewHandler(cfg, nil, store)
	handler.codexAuth = &stubCodexAuthManager{
		startResponse: codexauth.StartResponse{LoginID: "login-1"},
		statusResp:    codexauth.StatusResponse{LoginID: "login-1", Status: codexauth.StatusPending},
	}

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/endpoints/Codex/credentials/auth/start"},
		{http.MethodGet, "/api/endpoints/Codex/credentials/auth/login-1"},
		{http.MethodDelete, "/api/endpoints/Codex/credentials/auth/login-1"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s expected 401 without auth, got %d", tc.method, tc.path, rec.Code)
		}

		rec = httptest.NewRecorder()
		req = httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:secret")))
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s expected 200 with auth, got %d body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

func TestReloadConfigRebuildsCodexAuthManager(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	proxyInstance := proxy.New(cfg, nil, store, "test-device")
	handler := NewHandler(cfg, proxyInstance, store)
	originalAuth := handler.codexAuth

	cfg.UpdateProxy(&config.ProxyConfig{URL: "http://127.0.0.1:7890"})
	adapter := storage.NewConfigStorageAdapter(store)
	if err := cfg.SaveToStorage(adapter); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if err := handler.reloadConfig(); err != nil {
		t.Fatalf("reloadConfig returned error: %v", err)
	}
	if handler.codexAuth == nil {
		t.Fatal("expected codex auth manager to be available")
	}
	if handler.codexAuth == originalAuth {
		t.Fatal("expected reloadConfig to rebuild codex auth manager after proxy config changed")
	}
}

func newAPITestStorage(t *testing.T) *storage.SQLiteStorage {
	t.Helper()

	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func saveAPITestEndpoint(t *testing.T, store *storage.SQLiteStorage, ep storage.Endpoint) {
	t.Helper()
	if err := store.SaveEndpoint(&ep); err != nil {
		t.Fatalf("save endpoint: %v", err)
	}
}
