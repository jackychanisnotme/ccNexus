package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestImportEndpointCredentialsDefaultsClaudeOAuthProviderType(t *testing.T) {
	store := newAPITestStorage(t)
	saveAPITestEndpoint(t, store, storage.Endpoint{
		Name:        "ClaudeOAuth",
		APIUrl:      config.ClaudeOAuthTokenPoolAPIURL,
		AuthMode:    config.AuthModeClaudeOAuthTokenPool,
		Transformer: config.ClaudeOAuthTokenPoolTransformer,
		Model:       config.ClaudeOAuthTokenPoolDefaultModel,
		Enabled:     true,
	})

	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	handler := NewHandler(cfg, proxy.New(cfg, nil, store, "test-device"), store)

	rec := postJSON(t, handler, http.MethodPost, "/api/endpoints/ClaudeOAuth/credentials/import", map[string]any{
		"items": []map[string]any{
			{
				"access_token": "claude-oauth-token",
				"email":        "claude@example.com",
			},
		},
	})

	var resp SuccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode import response: %v", err)
	}

	credentials, err := store.GetEndpointCredentials("ClaudeOAuth")
	if err != nil {
		t.Fatalf("get credentials: %v", err)
	}
	if len(credentials) != 1 {
		t.Fatalf("expected one credential, got %d", len(credentials))
	}
	if credentials[0].ProviderType != storage.ProviderTypeClaudeOAuth {
		t.Fatalf("expected provider type %q, got %q", storage.ProviderTypeClaudeOAuth, credentials[0].ProviderType)
	}
}

func TestImportClaudeOAuthSetupTokenEndpoint(t *testing.T) {
	store := newAPITestStorage(t)
	saveAPITestEndpoint(t, store, storage.Endpoint{
		Name:        "ClaudeOAuth",
		APIUrl:      config.ClaudeOAuthTokenPoolAPIURL,
		AuthMode:    config.AuthModeClaudeOAuthTokenPool,
		Transformer: config.ClaudeOAuthTokenPoolTransformer,
		Model:       config.ClaudeOAuthTokenPoolDefaultModel,
		Enabled:     true,
	})

	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	handler := NewHandler(cfg, proxy.New(cfg, nil, store, "test-device"), store)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/endpoints/ClaudeOAuth/credentials/claude-oauth/import", bytes.NewReader([]byte(`{
		"token": "export CLAUDE_CODE_OAUTH_TOKEN='claude-setup-token'",
		"remark": "setup-token"
	}`)))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	credentials, err := store.GetEndpointCredentials("ClaudeOAuth")
	if err != nil {
		t.Fatalf("get credentials: %v", err)
	}
	if len(credentials) != 1 {
		t.Fatalf("expected one credential, got %d", len(credentials))
	}
	if credentials[0].ProviderType != storage.ProviderTypeClaudeOAuth {
		t.Fatalf("expected provider %q, got %q", storage.ProviderTypeClaudeOAuth, credentials[0].ProviderType)
	}
	if credentials[0].AccessToken != "claude-setup-token" {
		t.Fatalf("expected parsed setup token, got %q", credentials[0].AccessToken)
	}
}
