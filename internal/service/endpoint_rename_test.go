package service

import (
	"path/filepath"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestUpdateEndpointRenamePreservesTokenPool(t *testing.T) {
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	storedEndpoint := storage.Endpoint{
		Name:        "Codex Old",
		APIUrl:      config.CodexTokenPoolAPIURL,
		AuthMode:    config.AuthModeCodexTokenPool,
		Enabled:     true,
		Transformer: config.CodexTokenPoolTransformer,
		Model:       config.CodexTokenPoolDefaultModel,
		SortOrder:   0,
	}
	if err := store.SaveEndpoint(&storedEndpoint); err != nil {
		t.Fatalf("save endpoint: %v", err)
	}

	credential := storage.EndpointCredential{
		EndpointName: "Codex Old",
		ProviderType: storage.ProviderTypeCodex,
		AccountID:    "account-1",
		Email:        "codex@example.com",
		AccessToken:  "access-token",
		Status:       "active",
		Enabled:      true,
	}
	if err := store.SaveEndpointCredential(&credential); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	credentialID := credential.ID

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{
		Name:        "Codex Old",
		APIUrl:      config.CodexTokenPoolAPIURL,
		AuthMode:    config.AuthModeCodexTokenPool,
		Enabled:     true,
		Transformer: config.CodexTokenPoolTransformer,
		Model:       config.CodexTokenPoolDefaultModel,
	}})
	p := proxy.New(cfg, nil, store, "test-device")
	service := NewEndpointService(cfg, p, store)

	if err := service.UpdateEndpoint(
		0,
		"Codex New",
		config.CodexTokenPoolAPIURL,
		"",
		config.AuthModeCodexTokenPool,
		config.CodexTokenPoolTransformer,
		config.CodexTokenPoolDefaultModel,
		"",
		"",
		false,
		"",
	); err != nil {
		t.Fatalf("rename endpoint: %v", err)
	}

	renamedCredentials, err := store.GetEndpointCredentials("Codex New")
	if err != nil {
		t.Fatalf("get renamed credentials: %v", err)
	}
	if len(renamedCredentials) != 1 {
		t.Fatalf("renamed credentials = %#v, want one credential", renamedCredentials)
	}
	if renamedCredentials[0].ID != credentialID {
		t.Fatalf("renamed credential ID = %d, want %d", renamedCredentials[0].ID, credentialID)
	}

	oldCredentials, err := store.GetEndpointCredentials("Codex Old")
	if err != nil {
		t.Fatalf("get old credentials: %v", err)
	}
	if len(oldCredentials) != 0 {
		t.Fatalf("old credentials remain: %#v", oldCredentials)
	}

	storedEndpoints, err := store.GetEndpoints()
	if err != nil {
		t.Fatalf("get stored endpoints: %v", err)
	}
	if len(storedEndpoints) != 1 {
		t.Fatalf("stored endpoints = %#v, want one endpoint", storedEndpoints)
	}
	if storedEndpoints[0].Name != "Codex New" {
		t.Fatalf("stored endpoint name = %q, want %q", storedEndpoints[0].Name, "Codex New")
	}

	configEndpoints := cfg.GetEndpoints()
	if len(configEndpoints) != 1 {
		t.Fatalf("config endpoints = %#v, want one endpoint", configEndpoints)
	}
	if configEndpoints[0].Name != "Codex New" {
		t.Fatalf("config endpoint name = %q, want %q", configEndpoints[0].Name, "Codex New")
	}
}
