package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/service"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestAgentProvidersAPIStatusApplyAndRestore(t *testing.T) {
	home := t.TempDir()
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	cfg.UpdatePort(3456)
	handler := NewHandler(cfg, proxy.New(cfg, nil, store, "test-device"), store)
	handler.agentProvider = service.NewAgentProviderServiceWithOptions(cfg, service.AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	claudePath := filepath.Join(home, ".claude", "settings.json")
	writeAPITestFile(t, claudePath, `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com","ANTHROPIC_API_KEY":"old"}}`)

	rec := postJSON(t, handler, http.MethodPost, "/api/agent-providers/apply", map[string]any{
		"targets": []string{"claude"},
	})
	var applyResp SuccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &applyResp); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	applyData := applyResp.Data.(map[string]any)
	if applyData["backupId"] == "" {
		t.Fatalf("expected backupId in response: %#v", applyData)
	}

	rec = postJSON(t, handler, http.MethodPost, "/api/agent-providers/restore", map[string]any{
		"backupId": applyData["backupId"],
		"targets":  []string{"claude"},
	})
	var restoreResp SuccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &restoreResp); err != nil {
		t.Fatalf("decode restore response: %v", err)
	}
	if got := readAPITestFile(t, claudePath); got != `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com","ANTHROPIC_API_KEY":"old"}}` {
		t.Fatalf("expected claude config restored, got %s", got)
	}

	rec = httptestNewRequest(t, handler, http.MethodGet, "/api/agent-providers/status")
	var statusResp SuccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	status := statusResp.Data.(map[string]any)
	if status["targetUrl"] != "http://127.0.0.1:3456" {
		t.Fatalf("targetUrl = %#v", status["targetUrl"])
	}
}

func TestAgentProvidersAPIUsesStorageDataDirForBackups(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "server-data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	store, err := storage.NewSQLiteStorage(filepath.Join(dataDir, "ainexus.db"))
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	defer store.Close()
	t.Setenv("HOME", home)

	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	handler := NewHandler(cfg, proxy.New(cfg, nil, store, "test-device"), store)

	writeAPITestFile(t, filepath.Join(home, ".claude", "settings.json"), `{"env":{"ANTHROPIC_API_KEY":"old"}}`)
	rec := postJSON(t, handler, http.MethodPost, "/api/agent-providers/apply", map[string]any{
		"targets": []string{"claude"},
	})
	var applyResp SuccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &applyResp); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	applyData := applyResp.Data.(map[string]any)
	backupID, _ := applyData["backupId"].(string)
	if backupID == "" {
		t.Fatalf("expected backup id in response: %#v", applyData)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "agent-provider-backups", backupID, "manifest.json")); err != nil {
		t.Fatalf("expected backup manifest under storage data dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".AINexus", "agent-provider-backups", backupID, "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("backup should not be written under home data dir, stat err=%v", err)
	}
}

func writeAPITestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func readAPITestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return string(data)
}

func httptestNewRequest(t *testing.T, handler http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s status=%d body=%s", method, path, rec.Code, rec.Body.String())
	}
	return rec
}
