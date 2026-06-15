package main

import (
	"path/filepath"
	"testing"

	"github.com/lich0821/ccNexus/internal/codexauth"
	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/service"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestSaveSettingsRebuildsCodexAuthManagerAfterProxyChange(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)

	store, err := storage.NewSQLiteStorage(filepath.Join(tempDir, "ainexus.db"))
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.DefaultConfig()
	app := &App{
		config:   cfg,
		storage:  store,
		settings: service.NewSettingsService(cfg, store),
	}
	app.codexAuth = codexauth.NewManager(codexauth.Options{Storage: store})
	originalAuth := app.codexAuth

	settingsJSON := `{"proxyUrl":"http://127.0.0.1:7890","theme":"light","themeAuto":false,"claudeNotificationEnabled":false,"claudeNotificationType":"disabled"}`
	if err := app.SaveSettings(settingsJSON); err != nil {
		t.Fatalf("SaveSettings returned error: %v", err)
	}

	if app.codexAuth == nil {
		t.Fatal("expected codex auth manager to be available")
	}
	if app.codexAuth == originalAuth {
		t.Fatal("expected SaveSettings to rebuild codex auth manager after proxy config changed")
	}
	if proxy := cfg.GetProxy(); proxy == nil || proxy.URL != "http://127.0.0.1:7890" {
		t.Fatalf("expected proxy config to be saved, got %#v", proxy)
	}
}
