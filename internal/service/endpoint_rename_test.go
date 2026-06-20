package service

import (
	"path/filepath"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestUpdateEndpointRenamePreservesTokenPool(t *testing.T) {
	const (
		newName  = "Codex New"
		proxyURL = "http://127.0.0.1:7890"
		remark   = "renamed token pool"
	)

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
		newName+" ",
		config.CodexTokenPoolAPIURL,
		"",
		config.AuthModeCodexTokenPool,
		config.CodexTokenPoolTransformer,
		config.CodexTokenPoolDefaultModel,
		config.ThinkingHigh,
		proxyURL,
		true,
		remark,
	); err != nil {
		t.Fatalf("rename endpoint: %v", err)
	}

	configEndpoints := cfg.GetEndpoints()
	if len(configEndpoints) != 1 {
		t.Fatalf("config endpoints = %#v, want one endpoint", configEndpoints)
	}
	if configEndpoints[0].Name != newName {
		t.Fatalf("config endpoint name = %q, want %q", configEndpoints[0].Name, newName)
	}

	renamedCredentials, err := store.GetEndpointCredentials(newName)
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
	stored := storedEndpoints[0]
	if stored.Name != newName {
		t.Fatalf("stored endpoint name = %q, want %q", stored.Name, newName)
	}
	if stored.ProxyURL != proxyURL {
		t.Fatalf("stored endpoint proxy URL = %q, want %q", stored.ProxyURL, proxyURL)
	}
	if stored.Thinking != config.ThinkingHigh {
		t.Fatalf("stored endpoint thinking = %q, want %q", stored.Thinking, config.ThinkingHigh)
	}
	if !stored.ForceStream {
		t.Fatal("stored endpoint force stream = false, want true")
	}
	if stored.Remark != remark {
		t.Fatalf("stored endpoint remark = %q, want %q", stored.Remark, remark)
	}
}

func TestConfigStorageAdapterPreservesProxyURL(t *testing.T) {
	const (
		initialProxyURL = "http://127.0.0.1:7890"
		updatedProxyURL = "http://127.0.0.1:7891"
	)

	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	adapter := storage.NewConfigStorageAdapter(store)
	endpoint := config.StorageEndpoint{
		Name:        "Proxy Endpoint",
		APIUrl:      "https://api.example.com/v1",
		APIKey:      "test-key",
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "openai",
		ProxyURL:    initialProxyURL,
	}
	if err := adapter.SaveEndpoint(&endpoint); err != nil {
		t.Fatalf("save endpoint through adapter: %v", err)
	}

	storedEndpoints, err := store.GetEndpoints()
	if err != nil {
		t.Fatalf("get stored endpoints: %v", err)
	}
	if len(storedEndpoints) != 1 || storedEndpoints[0].ProxyURL != initialProxyURL {
		t.Fatalf("stored endpoints after save = %#v, want proxy URL %q", storedEndpoints, initialProxyURL)
	}

	adaptedEndpoints, err := adapter.GetEndpoints()
	if err != nil {
		t.Fatalf("get endpoints through adapter: %v", err)
	}
	if len(adaptedEndpoints) != 1 || adaptedEndpoints[0].ProxyURL != initialProxyURL {
		t.Fatalf("adapted endpoints = %#v, want proxy URL %q", adaptedEndpoints, initialProxyURL)
	}

	endpoint.ProxyURL = updatedProxyURL
	if err := adapter.UpdateEndpoint(&endpoint); err != nil {
		t.Fatalf("update endpoint through adapter: %v", err)
	}

	storedEndpoints, err = store.GetEndpoints()
	if err != nil {
		t.Fatalf("get updated stored endpoints: %v", err)
	}
	if len(storedEndpoints) != 1 || storedEndpoints[0].ProxyURL != updatedProxyURL {
		t.Fatalf("stored endpoints after update = %#v, want proxy URL %q", storedEndpoints, updatedProxyURL)
	}
}
