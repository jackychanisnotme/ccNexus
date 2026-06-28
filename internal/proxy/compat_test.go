package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestDiscoveryRouteRequiresLANModeAndExposesNoSecrets(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdatePort(43210)
	cfg.UpdateListenMode(config.ListenModeLocal)
	cfg.UpdateEndpoints([]config.Endpoint{
		{
			Name:        "SecretEndpoint",
			APIUrl:      "https://gateway.example.com",
			APIKey:      "test-key",
			AuthMode:    config.AuthModeAPIKey,
			Enabled:     true,
			Transformer: "openai2",
			Model:       "gpt-test",
		},
	})
	p := &Proxy{config: cfg}

	mux := http.NewServeMux()
	p.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/ainexus/discovery", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("local discovery status=%d body=%s", rec.Code, rec.Body.String())
	}

	cfg.UpdateListenMode(config.ListenModeLAN)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("LAN discovery status=%d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Product      string `json:"product"`
		Service     string `json:"service"`
		Version      int    `json:"version"`
		Port         int    `json:"port"`
		BaseURL      string `json:"baseUrl"`
		RequiresPair bool   `json:"requiresPairing"`
		Pairing      struct {
			Supported bool   `json:"supported"`
			Enabled   bool   `json:"enabled"`
			Method    string `json:"method"`
		} `json:"pairing"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode discovery response: %v body=%s", err, rec.Body.String())
	}
	if payload.Product != "AINexus" || payload.Service != "ainexus-proxy" || payload.Version != 1 {
		t.Fatalf("unexpected identity payload: %#v", payload)
	}
	if payload.Port != 43210 || payload.BaseURL != "http://127.0.0.1:43210" {
		t.Fatalf("unexpected address payload: %#v", payload)
	}
	if payload.RequiresPair || !payload.Pairing.Supported || payload.Pairing.Enabled || payload.Pairing.Method != "code-v1" {
		t.Fatalf("unexpected pairing reservation: %#v", payload)
	}
	if body := rec.Body.String(); body == "" || strings.Contains(body, "test-key") || strings.Contains(body, "SecretEndpoint") {
		t.Fatalf("discovery leaked endpoint detail: %s", body)
	}
}
