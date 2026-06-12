package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/storage"
)

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

func postJSON(t *testing.T, handler http.Handler, method, path string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s status=%d body=%s", method, path, rec.Code, rec.Body.String())
	}
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
