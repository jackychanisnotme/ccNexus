package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
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
