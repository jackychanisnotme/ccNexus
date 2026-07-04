package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestEndpointAPIRenamePreservesTokenPool(t *testing.T) {
	const (
		oldName      = "Codex Old"
		newName      = "Codex New"
		credentialID = "stable-account"
	)

	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	proxyInstance := proxy.New(cfg, nil, store, "test-device")
	handler := NewHandler(cfg, proxyInstance, store)

	saveAPITestEndpoint(t, store, storage.Endpoint{
		Name:        oldName,
		APIUrl:      config.CodexTokenPoolAPIURL,
		AuthMode:    config.AuthModeCodexTokenPool,
		Enabled:     true,
		Transformer: config.CodexTokenPoolTransformer,
		Model:       config.CodexTokenPoolDefaultModel,
	})
	credential := storage.EndpointCredential{
		EndpointName: oldName,
		ProviderType: storage.ProviderTypeCodex,
		AccountID:    credentialID,
		Email:        "codex@example.com",
		AccessToken:  "access-token",
		Status:       "active",
		Enabled:      true,
	}
	if err := store.SaveEndpointCredential(&credential); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	originalCredentialID := credential.ID

	postJSON(t, handler, http.MethodPut, "/api/endpoints/Codex%20Old", map[string]any{
		"name":        " " + newName + " ",
		"apiUrl":      config.CodexTokenPoolAPIURL,
		"authMode":    config.AuthModeCodexTokenPool,
		"enabled":     true,
		"transformer": config.CodexTokenPoolTransformer,
		"model":       config.CodexTokenPoolDefaultModel,
		"thinking":    config.ThinkingHigh,
	})

	endpoints, err := store.GetEndpoints()
	if err != nil {
		t.Fatalf("get endpoints: %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("stored endpoints = %#v, want one renamed endpoint", endpoints)
	}
	if endpoints[0].Name != newName {
		t.Fatalf("stored endpoint name = %q, want %q", endpoints[0].Name, newName)
	}
	if endpoints[0].AuthMode != config.AuthModeCodexTokenPool {
		t.Fatalf("stored auth mode = %q, want %q", endpoints[0].AuthMode, config.AuthModeCodexTokenPool)
	}
	if endpoints[0].APIUrl != config.CodexTokenPoolAPIURL {
		t.Fatalf("stored api URL = %q, want %q", endpoints[0].APIUrl, config.CodexTokenPoolAPIURL)
	}
	if endpoints[0].Transformer != config.CodexTokenPoolTransformer {
		t.Fatalf("stored transformer = %q, want %q", endpoints[0].Transformer, config.CodexTokenPoolTransformer)
	}

	newCredentials, err := store.GetEndpointCredentials(newName)
	if err != nil {
		t.Fatalf("get new credentials: %v", err)
	}
	if len(newCredentials) != 1 {
		t.Fatalf("new credentials = %#v, want one credential", newCredentials)
	}
	if newCredentials[0].ID != originalCredentialID {
		t.Fatalf("credential ID = %d, want original ID %d", newCredentials[0].ID, originalCredentialID)
	}
	if newCredentials[0].AccountID != credentialID {
		t.Fatalf("credential account ID = %q, want %q", newCredentials[0].AccountID, credentialID)
	}
	oldCredentials, err := store.GetEndpointCredentials(oldName)
	if err != nil {
		t.Fatalf("get old credentials: %v", err)
	}
	if len(oldCredentials) != 0 {
		t.Fatalf("old credentials = %#v, want none", oldCredentials)
	}

	configEndpoints := handler.config.GetEndpoints()
	if len(configEndpoints) != 1 {
		t.Fatalf("config endpoints = %#v, want one renamed endpoint", configEndpoints)
	}
	if configEndpoints[0].Name != newName {
		t.Fatalf("config endpoint name = %q, want %q", configEndpoints[0].Name, newName)
	}
}

func TestEndpointAPIRenamePreservesCurrentEndpoint(t *testing.T) {
	const (
		firstName   = "First"
		oldName     = "Second"
		newName     = "Renamed"
		secondURL   = "https://second.example.com"
		secondKey   = "second-key"
		secondModel = "second-model"
	)

	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	proxyInstance := proxy.New(cfg, nil, store, "test-device")
	handler := NewHandler(cfg, proxyInstance, store)

	saveAPITestEndpoint(t, store, storage.Endpoint{
		Name:        firstName,
		APIUrl:      "https://first.example.com",
		APIKey:      "first-key",
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "openai",
		Model:       "first-model",
		SortOrder:   0,
	})
	saveAPITestEndpoint(t, store, storage.Endpoint{
		Name:        oldName,
		APIUrl:      secondURL,
		APIKey:      secondKey,
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "openai",
		Model:       secondModel,
		SortOrder:   1,
	})
	if err := handler.reloadConfig(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := proxyInstance.SetCurrentEndpoint(oldName); err != nil {
		t.Fatalf("set current endpoint: %v", err)
	}

	postJSON(t, handler, http.MethodPut, "/api/endpoints/"+oldName, map[string]any{
		"name":        newName,
		"apiUrl":      secondURL,
		"apiKey":      secondKey,
		"authMode":    config.AuthModeAPIKey,
		"enabled":     true,
		"transformer": "openai",
		"model":       secondModel,
	})

	if got := proxyInstance.GetCurrentEndpointName(); got != newName {
		t.Fatalf("proxy current endpoint = %q, want renamed endpoint %q", got, newName)
	}
	renamed := mustGetStoredEndpoint(t, store, newName)
	if renamed.APIUrl != secondURL || renamed.APIKey != secondKey || renamed.Model != secondModel {
		t.Fatalf("stored renamed endpoint = %#v", renamed)
	}
	foundRenamed := false
	for _, endpoint := range handler.config.GetEndpoints() {
		if endpoint.Name == oldName {
			t.Fatalf("config still contains old endpoint name %q", oldName)
		}
		if endpoint.Name == newName {
			foundRenamed = true
		}
	}
	if !foundRenamed {
		t.Fatalf("config endpoints = %#v, want renamed endpoint %q", handler.config.GetEndpoints(), newName)
	}
}

func TestEndpointAPIRenameRejectsActiveCollision(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	proxyInstance := proxy.New(cfg, nil, store, "test-device")
	handler := NewHandler(cfg, proxyInstance, store)

	saveAPITestEndpoint(t, store, storage.Endpoint{
		Name:        "Source",
		APIUrl:      "https://source.example.com",
		APIKey:      "source-key",
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "openai",
		Model:       "source-model",
		SortOrder:   0,
	})
	saveAPITestEndpoint(t, store, storage.Endpoint{
		Name:        "Destination",
		APIUrl:      "https://destination.example.com",
		APIKey:      "destination-key",
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "openai",
		Model:       "destination-model",
		SortOrder:   1,
	})

	rec := requestJSON(t, handler, http.MethodPut, "/api/endpoints/Source", map[string]any{
		"name":        "Destination",
		"apiUrl":      "https://source-renamed.example.com",
		"apiKey":      "source-key",
		"authMode":    config.AuthModeAPIKey,
		"enabled":     true,
		"transformer": "openai",
		"model":       "updated-model",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("PUT collision status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}

	source := mustGetStoredEndpoint(t, store, "Source")
	if source.APIUrl != "https://source.example.com" || source.Model != "source-model" {
		t.Fatalf("source endpoint changed after collision: %#v", source)
	}
	destination := mustGetStoredEndpoint(t, store, "Destination")
	if destination.APIUrl != "https://destination.example.com" || destination.Model != "destination-model" {
		t.Fatalf("destination endpoint changed after collision: %#v", destination)
	}
}

func TestEndpointAPIRenameRejectsWhitespaceOnlyName(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	proxyInstance := proxy.New(cfg, nil, store, "test-device")
	handler := NewHandler(cfg, proxyInstance, store)

	saveAPITestEndpoint(t, store, storage.Endpoint{
		Name:        "Source",
		APIUrl:      "https://source.example.com",
		APIKey:      "source-key",
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "openai",
		Model:       "source-model",
		Remark:      "source-remark",
		ProxyURL:    "http://127.0.0.1:7890",
	})
	original := mustGetStoredEndpoint(t, store, "Source")

	rec := requestJSON(t, handler, http.MethodPut, "/api/endpoints/Source", map[string]any{
		"name":        "   ",
		"apiUrl":      "https://changed.example.com",
		"apiKey":      "changed-key",
		"authMode":    config.AuthModeAPIKey,
		"enabled":     false,
		"transformer": "gemini",
		"model":       "changed-model",
		"remark":      "changed-remark",
		"proxyUrl":    "http://127.0.0.1:7891",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT whitespace-only rename status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}

	endpoint := mustGetStoredEndpoint(t, store, "Source")
	if endpoint != original {
		t.Fatalf("endpoint changed after whitespace-only rename: got %#v, want %#v", endpoint, original)
	}
}

func TestClassifyEndpointRenameError(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantStatus     int
		wantMessage    string
		forbiddenTexts []string
	}{
		{
			name:        "destination conflict",
			err:         fmt.Errorf("rename destination: %w", storage.ErrEndpointNameConflict),
			wantStatus:  http.StatusConflict,
			wantMessage: "Endpoint with this name already exists",
		},
		{
			name:        "source missing",
			err:         fmt.Errorf("lookup source: %w", storage.ErrEndpointNotFound),
			wantStatus:  http.StatusNotFound,
			wantMessage: "Endpoint not found",
		},
		{
			name:        "validation error",
			err:         fmt.Errorf("validate rename: %w", storage.ErrInvalidEndpointName),
			wantStatus:  http.StatusBadRequest,
			wantMessage: "Invalid endpoint name",
		},
		{
			name:        "same names validation error",
			err:         fmt.Errorf("validate different names: %w", storage.ErrInvalidEndpointName),
			wantStatus:  http.StatusBadRequest,
			wantMessage: "Invalid endpoint name",
		},
		{
			name:           "internal database error",
			err:            fmt.Errorf("commit endpoint rename transaction: %s", "database is locked; table endpoint_credentials"),
			wantStatus:     http.StatusInternalServerError,
			wantMessage:    "Failed to rename endpoint",
			forbiddenTexts: []string{"database is locked", "endpoint_credentials"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, message := classifyEndpointRenameError(tt.err)
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d", status, tt.wantStatus)
			}
			if message != tt.wantMessage {
				t.Fatalf("message = %q, want %q", message, tt.wantMessage)
			}
			for _, forbidden := range tt.forbiddenTexts {
				if strings.Contains(message, forbidden) {
					t.Fatalf("message %q exposes forbidden text %q", message, forbidden)
				}
			}
		})
	}
}

func TestEndpointAPIProxyURLPersistsThroughCreateUpdateAndClone(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	proxyInstance := proxy.New(cfg, nil, store, "test-device")
	handler := NewHandler(cfg, proxyInstance, store)

	postJSON(t, handler, http.MethodPost, "/api/endpoints", map[string]any{
		"name":        "Source",
		"apiUrl":      "https://api.example.com",
		"apiKey":      "source-key",
		"authMode":    config.AuthModeAPIKey,
		"enabled":     true,
		"transformer": "openai",
		"model":       "proxy-url-model",
		"proxyUrl":    "http://127.0.0.1:7890",
	})

	source := mustGetStoredEndpoint(t, store, "Source")
	if source.ProxyURL != "http://127.0.0.1:7890" {
		t.Fatalf("expected created endpoint proxy URL to persist, got %q", source.ProxyURL)
	}

	postJSON(t, handler, http.MethodPut, "/api/endpoints/Source", map[string]any{
		"name":        "Source",
		"apiUrl":      "https://api.example.com",
		"authMode":    config.AuthModeAPIKey,
		"enabled":     true,
		"transformer": "openai",
		"model":       "proxy-url-model",
		"proxyUrl":    "http://127.0.0.1:7891",
	})

	source = mustGetStoredEndpoint(t, store, "Source")
	if source.ProxyURL != "http://127.0.0.1:7891" {
		t.Fatalf("expected updated endpoint proxy URL to persist, got %q", source.ProxyURL)
	}

	postJSON(t, handler, http.MethodPost, "/api/endpoints", map[string]any{
		"name":        "Clone",
		"apiUrl":      "https://clone.example.com",
		"authMode":    config.AuthModeAPIKey,
		"enabled":     true,
		"transformer": "openai",
		"cloneFrom":   "Source",
	})

	clone := mustGetStoredEndpoint(t, store, "Clone")
	if clone.ProxyURL != "http://127.0.0.1:7891" {
		t.Fatalf("expected cloned endpoint to inherit proxy URL, got %q", clone.ProxyURL)
	}
}

func TestEndpointAPIMaxConcurrentRequestsPersistsThroughCreateUpdateAndList(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	proxyInstance := proxy.New(cfg, nil, store, "test-device")
	handler := NewHandler(cfg, proxyInstance, store)

	postJSON(t, handler, http.MethodPost, "/api/endpoints", map[string]any{
		"name":                  "Limited",
		"apiUrl":                "https://api.example.com",
		"apiKey":                "source-key",
		"authMode":              config.AuthModeAPIKey,
		"enabled":               true,
		"transformer":           "openai",
		"model":                 "limited-model",
		"maxConcurrentRequests": 2,
	})

	source := mustGetStoredEndpoint(t, store, "Limited")
	if source.MaxConcurrentRequests != 2 {
		t.Fatalf("expected created endpoint max concurrent requests to persist, got %d", source.MaxConcurrentRequests)
	}

	postJSON(t, handler, http.MethodPut, "/api/endpoints/Limited", map[string]any{
		"name":                  "Limited",
		"apiUrl":                "https://api.example.com",
		"authMode":              config.AuthModeAPIKey,
		"enabled":               true,
		"transformer":           "openai",
		"model":                 "limited-model",
		"maxConcurrentRequests": 4,
	})

	source = mustGetStoredEndpoint(t, store, "Limited")
	if source.MaxConcurrentRequests != 4 {
		t.Fatalf("expected updated endpoint max concurrent requests to persist, got %d", source.MaxConcurrentRequests)
	}

	rec := requestJSON(t, handler, http.MethodGet, "/api/endpoints", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/endpoints status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp SuccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := resp.Data.(map[string]any)
	endpoints := data["endpoints"].([]any)
	if len(endpoints) != 1 {
		t.Fatalf("expected one listed endpoint, got %#v", endpoints)
	}
	listed := endpoints[0].(map[string]any)
	if got := int(listed["maxConcurrentRequests"].(float64)); got != 4 {
		t.Fatalf("listed max concurrent requests = %d, want 4", got)
	}
}

func TestFetchModelsUsesProxyURLFromRequest(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	handler := NewHandler(cfg, nil, store)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "direct upstream should not be used", http.StatusInternalServerError)
	}))
	defer upstream.Close()

	var proxyHits int32
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&proxyHits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-through-proxy"}]}`))
	}))
	defer proxyServer.Close()

	rec := postJSON(t, handler, http.MethodPost, "/api/endpoints/fetch-models", map[string]any{
		"apiUrl":      upstream.URL,
		"apiKey":      "test-key",
		"transformer": "openai",
		"proxyUrl":    proxyServer.URL,
	})

	var resp SuccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := resp.Data.(map[string]any)
	models := data["models"].([]any)
	if len(models) != 1 || models[0] != "model-through-proxy" {
		t.Fatalf("expected model list from proxy response, got %#v", models)
	}
	if atomic.LoadInt32(&proxyHits) == 0 {
		t.Fatal("expected fetch models request to go through endpoint proxy")
	}
}

func TestSwitchEndpointActuallyChangesCurrentEndpoint(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	proxyInstance := proxy.New(cfg, nil, store, "test-device")
	handler := NewHandler(cfg, proxyInstance, store)

	saveAPITestEndpoint(t, store, storage.Endpoint{
		Name:        "A",
		APIUrl:      "https://a.example.com",
		APIKey:      "key-a",
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "openai",
		Model:       "model-a",
	})
	saveAPITestEndpoint(t, store, storage.Endpoint{
		Name:        "B",
		APIUrl:      "https://b.example.com",
		APIKey:      "key-b",
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "openai",
		Model:       "model-b",
	})
	if err := handler.reloadConfig(); err != nil {
		t.Fatalf("reload config: %v", err)
	}

	rec := requestJSON(t, handler, http.MethodPost, "/api/endpoints/switch", map[string]any{"name": "B"})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/endpoints/switch status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := proxyInstance.GetCurrentEndpointName(); got != "B" {
		t.Fatalf("current endpoint = %q, want %q", got, "B")
	}
}

func TestEndpointAPICreateRejectsInvalidTransformerConfig(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	proxyInstance := proxy.New(cfg, nil, store, "test-device")
	handler := NewHandler(cfg, proxyInstance, store)

	rec := requestJSON(t, handler, http.MethodPost, "/api/endpoints", map[string]any{
		"name":        "Broken",
		"apiUrl":      "https://api.example.com",
		"apiKey":      "key",
		"authMode":    config.AuthModeAPIKey,
		"enabled":     true,
		"transformer": "openai2",
		"model":       "",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing model, got %d body=%s", rec.Code, rec.Body.String())
	}
	endpoints, err := store.GetEndpoints()
	if err != nil {
		t.Fatalf("get endpoints: %v", err)
	}
	if len(endpoints) != 0 {
		t.Fatalf("invalid endpoint was saved: %#v", endpoints)
	}

	rec = requestJSON(t, handler, http.MethodPost, "/api/endpoints", map[string]any{
		"name":        "Unknown",
		"apiUrl":      "https://api.example.com",
		"apiKey":      "key",
		"authMode":    config.AuthModeAPIKey,
		"enabled":     true,
		"transformer": "unknown-transformer",
		"model":       "model",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown transformer, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEndpointAPIUpdateRejectsInvalidTransformerConfigWithoutSaving(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	proxyInstance := proxy.New(cfg, nil, store, "test-device")
	handler := NewHandler(cfg, proxyInstance, store)

	saveAPITestEndpoint(t, store, storage.Endpoint{
		Name:        "Good",
		APIUrl:      "https://api.example.com",
		APIKey:      "key",
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "claude",
	})

	rec := requestJSON(t, handler, http.MethodPut, "/api/endpoints/Good", map[string]any{
		"name":        "Good",
		"apiUrl":      "https://api.example.com",
		"apiKey":      "key",
		"authMode":    config.AuthModeAPIKey,
		"enabled":     true,
		"transformer": "openai2",
		"model":       "",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing model, got %d body=%s", rec.Code, rec.Body.String())
	}
	stored := mustGetStoredEndpoint(t, store, "Good")
	if stored.Transformer != "claude" || stored.Model != "" {
		t.Fatalf("invalid update was saved: %#v", stored)
	}
}

func postJSON(t *testing.T, handler http.Handler, method, path string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()

	rec := requestJSON(t, handler, method, path, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s status=%d body=%s", method, path, rec.Code, rec.Body.String())
	}
	return rec
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	return rec
}

func mustGetStoredEndpoint(t *testing.T, store *storage.SQLiteStorage, name string) storage.Endpoint {
	t.Helper()

	endpoints, err := store.GetEndpoints()
	if err != nil {
		t.Fatalf("get endpoints: %v", err)
	}
	for _, ep := range endpoints {
		if ep.Name == name {
			return ep
		}
	}
	t.Fatalf("endpoint %q not found", name)
	return storage.Endpoint{}
}
