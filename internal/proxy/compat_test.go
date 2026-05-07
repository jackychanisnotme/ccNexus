package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
)

func TestCompatibilityModelRoutesDoNotRequireRequestBody(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		{
			Name:        "DeepSeekGateway",
			APIUrl:      "https://gateway.example.com",
			APIKey:      "test-key",
			AuthMode:    config.AuthModeAPIKey,
			Enabled:     true,
			Transformer: "deepseek",
			Model:       "deepseek-v4-pro",
		},
	})
	p := &Proxy{
		config:      cfg,
		modelsCache: NewModelsCache(30),
	}
	p.modelsCache.Set([]ModelInfo{
		{ID: "deepseek-v4-pro", Object: "model", OwnedBy: "deepseek", EndpointID: "DeepSeekGateway"},
	})

	mux := http.NewServeMux()
	p.registerRoutes(mux)

	for _, path := range []string{"/models", "/api/v1/models"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected HTTP 200, got %d body=%q", path, rec.Code, rec.Body.String())
		}
		var payload struct {
			Data []ModelInfo `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("%s: failed to decode response: %v", path, err)
		}
		if len(payload.Data) != 1 || payload.Data[0].ID != "deepseek-v4-pro" {
			t.Fatalf("%s: unexpected models response: %#v", path, payload)
		}
	}
}

func TestCompatibilityProbeRoutes(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		{
			Name:        "DeepSeekGateway",
			APIUrl:      "https://gateway.example.com",
			APIKey:      "test-key",
			AuthMode:    config.AuthModeAPIKey,
			Enabled:     true,
			Transformer: "deepseek",
			Model:       "deepseek-v4-pro",
		},
	})
	p := &Proxy{
		config:       cfg,
		modelsCache:  NewModelsCache(30),
		currentIndex: 0,
	}
	p.modelsCache.Set([]ModelInfo{
		{ID: "deepseek-v4-pro", Object: "model", OwnedBy: "deepseek", EndpointID: "DeepSeekGateway"},
	})

	mux := http.NewServeMux()
	p.registerRoutes(mux)

	for _, path := range []string{"/api/tags", "/version", "/props", "/v1/props"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected HTTP 200, got %d body=%q", path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%s: expected JSON content type, got %q", path, rec.Header().Get("Content-Type"))
		}
	}
}
