package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/service"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestAppAgentBindingsReturnJSONErrorsBeforeStartup(t *testing.T) {
	app := NewApp(nil)

	raw := app.RunAgent(`{"task":""}`)
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("invalid json: %v raw=%s", err, raw)
	}
	if result["success"] != false {
		t.Fatalf("expected failure before startup, got %#v", result)
	}
}

func TestActivateEndpointCredentialClearsCooldownState(t *testing.T) {
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{
		Name:        "Pool",
		APIUrl:      config.CodexTokenPoolAPIURL,
		AuthMode:    config.AuthModeCodexTokenPool,
		Enabled:     true,
		Transformer: config.CodexTokenPoolTransformer,
		Model:       config.CodexTokenPoolDefaultModel,
	}})
	app := NewApp(nil)
	app.config = cfg
	app.storage = store

	cooldownUntil := time.Now().UTC().Add(time.Hour)
	cred := storage.EndpointCredential{
		EndpointName:  "Pool",
		ProviderType:  storage.ProviderTypeCodex,
		AccessToken:   "access-token",
		Status:        "active",
		Enabled:       true,
		FailureCount:  3,
		CooldownUntil: &cooldownUntil,
		LastError:     "rate limited",
		LastCheckedAt: &cooldownUntil,
	}
	if err := store.SaveEndpointCredential(&cred); err != nil {
		t.Fatalf("save credential: %v", err)
	}

	if err := app.ActivateEndpointCredential(0, cred.ID); err != nil {
		t.Fatalf("activate credential: %v", err)
	}
	updated, err := store.GetCredentialByID(cred.ID)
	if err != nil {
		t.Fatalf("get credential: %v", err)
	}
	if updated.Status != "active" || updated.FailureCount != 0 || updated.CooldownUntil != nil || updated.LastError != "" {
		t.Fatalf("activation did not clear cooldown state: %#v", updated)
	}
}

func TestCodexResetCreditBindingsReturnJSONErrors(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{
		Name:     "OpenAI",
		APIUrl:   "https://api.openai.com",
		AuthMode: config.AuthModeAPIKey,
		Enabled:  true,
	}})
	app := NewApp(nil)
	app.config = cfg

	for name, raw := range map[string]string{
		"get":     app.GetCodexResetCredits(0, 1),
		"consume": app.ConsumeCodexResetCredit(0, 1),
	} {
		var result map[string]any
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatalf("%s invalid json: %v raw=%s", name, err, raw)
		}
		if result["success"] != false {
			t.Fatalf("%s expected failure, got %#v", name, result)
		}
		if result["error"] == "" {
			t.Fatalf("%s expected error message, got %#v", name, result)
		}
	}
}

func TestFetchCodexRateLimitsForEndpointReturnsJSONErrors(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{
		Name:     "OpenAI",
		APIUrl:   "https://api.openai.com",
		AuthMode: config.AuthModeAPIKey,
		Enabled:  true,
	}, {
		Name:        "Codex Pool",
		APIUrl:      config.CodexTokenPoolAPIURL,
		AuthMode:    config.AuthModeCodexTokenPool,
		Enabled:     true,
		Transformer: config.CodexTokenPoolTransformer,
		Model:       config.CodexTokenPoolDefaultModel,
	}})
	app := NewApp(nil)
	app.config = cfg

	cases := map[string]string{
		"empty":    app.FetchCodexRateLimitsForEndpoint(" "),
		"notFound": app.FetchCodexRateLimitsForEndpoint("Missing Pool"),
		"nonCodex": app.FetchCodexRateLimitsForEndpoint("OpenAI"),
		"noProxy":  app.FetchCodexRateLimitsForEndpoint("Codex Pool"),
	}
	for name, raw := range cases {
		var result map[string]any
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatalf("%s invalid json: %v raw=%s", name, err, raw)
		}
		if result["success"] != false {
			t.Fatalf("%s expected failure, got %#v", name, result)
		}
		if result["error"] == "" {
			t.Fatalf("%s expected error message, got %#v", name, result)
		}
	}
}

func TestAddDiscoveredLANEndpointCreatesEnabledCompatibleEndpoint(t *testing.T) {
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{
		Name:        "Existing",
		APIUrl:      "https://api.example.com",
		APIKey:      "key",
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "openai2",
		Model:       "gpt-test",
	}})
	app := NewApp(nil)
	app.config = cfg
	app.storage = store
	app.proxy = proxy.New(cfg, nil, store, "device-a")
	app.endpoint = service.NewEndpointService(cfg, app.proxy, store)

	raw := app.AddDiscoveredLANEndpoint(`{"id":"node-1","name":"Office Mac","baseUrl":"http://192.168.1.20:3000","host":"192.168.1.20","port":3000}`)
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("invalid json: %v raw=%s", err, raw)
	}
	if result["success"] != true {
		t.Fatalf("expected success, got %#v", result)
	}

	endpoints := cfg.GetEndpoints()
	if len(endpoints) != 2 {
		t.Fatalf("endpoint count = %d, want 2", len(endpoints))
	}
	added := endpoints[1]
	if added.Name != "局域网 AINexus - Office Mac" ||
		added.APIUrl != "http://192.168.1.20:3000" ||
		added.APIKey != "ainexus-lan-unpaired" ||
		added.AuthMode != config.AuthModeAPIKey ||
		added.Transformer != "openai2" ||
		added.Model != "gpt-5.5" ||
		!added.Enabled {
		t.Fatalf("unexpected LAN endpoint: %#v", added)
	}
	if added.Remark != "AINexus LAN discovery; pairing reserved but disabled" {
		t.Fatalf("unexpected remark: %q", added.Remark)
	}
}
