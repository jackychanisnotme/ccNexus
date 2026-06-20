package service

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestUpdateEndpointRejectsNormalizedDuplicateName(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		{
			Name:        "Source",
			APIUrl:      "https://source.example.com/v1",
			APIKey:      "source-key",
			AuthMode:    config.AuthModeAPIKey,
			Enabled:     true,
			Transformer: "openai",
			Model:       "source-model",
		},
		{
			Name:        "Destination ",
			APIUrl:      "https://destination.example.com/v1",
			APIKey:      "destination-key",
			AuthMode:    config.AuthModeAPIKey,
			Enabled:     true,
			Transformer: "openai",
			Model:       "destination-model",
		},
	})
	p := proxy.New(cfg, nil, nil, "test-device")
	service := NewEndpointService(cfg, p, nil)

	err := service.UpdateEndpoint(
		0,
		" Destination ",
		"https://renamed.example.com/v1",
		"renamed-key",
		config.AuthModeAPIKey,
		"openai",
		"renamed-model",
		"",
		"",
		false,
		"renamed",
	)
	if err == nil || err.Error() != "endpoint name 'Destination' already exists" {
		t.Fatalf("UpdateEndpoint() error = %v, want normalized duplicate error", err)
	}

	endpoints := cfg.GetEndpoints()
	if len(endpoints) != 2 ||
		endpoints[0].Name != "Source" ||
		endpoints[0].APIUrl != "https://source.example.com/v1" ||
		endpoints[1].Name != "Destination " {
		t.Fatalf("config changed after rejected normalized duplicate: %#v", endpoints)
	}
	if current := p.GetCurrentEndpointName(); current != "Source" {
		t.Fatalf("proxy current endpoint = %q, want Source", current)
	}
}

func TestUpdateEndpointAllowsTrimmingCurrentLegacyName(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{
		Name:        "Legacy ",
		APIUrl:      "https://legacy.example.com/v1",
		APIKey:      "legacy-key",
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "openai",
		Model:       "legacy-model",
	}})
	p := proxy.New(cfg, nil, nil, "test-device")
	service := NewEndpointService(cfg, p, nil)

	if err := service.UpdateEndpoint(
		0,
		"Legacy",
		"https://legacy.example.com/v1",
		"legacy-key",
		config.AuthModeAPIKey,
		"openai",
		"legacy-model",
		"",
		"",
		false,
		"",
	); err != nil {
		t.Fatalf("UpdateEndpoint() error = %v, want legacy self-trim to succeed", err)
	}

	endpoints := cfg.GetEndpoints()
	if len(endpoints) != 1 || endpoints[0].Name != "Legacy" {
		t.Fatalf("config endpoints = %#v, want trimmed legacy name", endpoints)
	}
	if current := p.GetCurrentEndpointName(); current != "Legacy" {
		t.Fatalf("proxy current endpoint = %q, want Legacy", current)
	}
}

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

func TestUpdateEndpointRenameFailureLeavesLiveConfigAndProxyUnchanged(t *testing.T) {
	const (
		oldName       = "Codex Old"
		storedName    = "Codex Legacy"
		newName       = "Codex New"
		updatedRemark = "should not reach live config"
	)

	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	storedEndpoint := storage.Endpoint{
		Name:        storedName,
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

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{
		Name:        oldName,
		APIUrl:      config.CodexTokenPoolAPIURL,
		AuthMode:    config.AuthModeCodexTokenPool,
		Enabled:     true,
		Transformer: config.CodexTokenPoolTransformer,
		Model:       config.CodexTokenPoolDefaultModel,
	}})
	p := proxy.New(cfg, nil, store, "test-device")
	service := NewEndpointService(cfg, p, store)

	err = service.UpdateEndpoint(
		0,
		newName,
		config.CodexTokenPoolAPIURL,
		"",
		config.AuthModeCodexTokenPool,
		config.CodexTokenPoolTransformer,
		config.CodexTokenPoolDefaultModel,
		config.ThinkingHigh,
		"",
		true,
		updatedRemark,
	)
	if !errors.Is(err, storage.ErrEndpointNotFound) {
		t.Fatalf("rename endpoint error = %v, want errors.Is(_, ErrEndpointNotFound)", err)
	}

	configEndpoints := cfg.GetEndpoints()
	if len(configEndpoints) != 1 {
		t.Fatalf("config endpoints = %#v, want one endpoint", configEndpoints)
	}
	if configEndpoints[0].Name != oldName {
		t.Fatalf("config endpoint name = %q, want %q", configEndpoints[0].Name, oldName)
	}
	if configEndpoints[0].Remark == updatedRemark {
		t.Fatalf("config endpoint remark = %q, want unchanged remark", configEndpoints[0].Remark)
	}
	if current := p.GetCurrentEndpointName(); current != oldName {
		t.Fatalf("proxy current endpoint = %q, want %q", current, oldName)
	}

	storedEndpoints, err := store.GetEndpoints()
	if err != nil {
		t.Fatalf("get stored endpoints: %v", err)
	}
	if len(storedEndpoints) != 1 {
		t.Fatalf("stored endpoints = %#v, want one endpoint", storedEndpoints)
	}
	if storedEndpoints[0].Name != storedName {
		t.Fatalf("stored endpoint name = %q, want %q", storedEndpoints[0].Name, storedName)
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
