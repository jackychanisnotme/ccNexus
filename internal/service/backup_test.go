package service

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestCloudBackupRequiresLoginCredentialOptInForClaudeOAuth(t *testing.T) {
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.SaveEndpointCredential(&storage.EndpointCredential{
		EndpointName: "ClaudeOAuth",
		ProviderType: storage.ProviderTypeClaudeOAuth,
		AccessToken:  "claude-oauth-token",
		Status:       "active",
		Enabled:      true,
	}); err != nil {
		t.Fatalf("save credential: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.UpdateBackup(&config.BackupConfig{Provider: string(BackupProviderWebDAV)})
	backup := NewBackupService(cfg, store, "test", nil)

	err = backup.BackupToProvider(string(BackupProviderWebDAV), "backup.db")
	if err == nil || !strings.Contains(err.Error(), "backup_login_credentials_opt_in_required") {
		t.Fatalf("expected login credential opt-in error, got %v", err)
	}

	cfg.UpdateBackup(&config.BackupConfig{
		Provider:                string(BackupProviderWebDAV),
		IncludeLoginCredentials: true,
	})
	err = backup.BackupToProvider(string(BackupProviderWebDAV), "backup.db")
	if err == nil || !strings.Contains(err.Error(), "webdav_not_configured") {
		t.Fatalf("expected webdav configuration error after opt-in, got %v", err)
	}
}
