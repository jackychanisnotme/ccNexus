package service

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestTerminalServiceRepairCodexSessionVisibilityJSON(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, ".codex")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeFile(t, filepath.Join(dataDir, "config.toml"), `model_provider = "AINexus"`+"\n")
	dbPath := filepath.Join(dataDir, "sqlite", "state_5.sqlite")
	rolloutPath := filepath.Join(dataDir, "sessions", "2026", "06", "17", "rollout-thread-1.jsonl")
	terminalCreateVisibilityDB(t, dbPath)
	terminalInsertVisibilityThread(t, dbPath, "thread-1", terminalRel(t, dataDir, rolloutPath))
	writeFile(t, rolloutPath, `{"type":"session_meta","payload":{"id":"thread-1","model_provider":"old"}}`+"\n")

	svc := NewTerminalService(nil, nil)
	raw := svc.RepairCodexSessionVisibility(`{"mode":"quick","sessionScope":"selected","sessionIds":["thread-1"]}`)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			TargetProvider          string `json:"targetProvider"`
			UpdatedSqliteRowCount   int    `json:"updatedSqliteRowCount"`
			ChangedRolloutFileCount int    `json:"changedRolloutFileCount"`
			BackupDir               string `json:"backupDir"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatalf("decode response: %v\n%s", err, raw)
	}
	if !response.Success {
		t.Fatalf("repair failed: %s", response.Error)
	}
	if response.Data.TargetProvider != "AINexus" || response.Data.UpdatedSqliteRowCount != 1 || response.Data.ChangedRolloutFileCount != 1 {
		t.Fatalf("unexpected response: %#v", response.Data)
	}
	if response.Data.BackupDir == "" {
		t.Fatal("expected backup dir in response")
	}
}

func TestTerminalServiceRepairCodexSessionVisibilityRejectsInvalidJSON(t *testing.T) {
	svc := NewTerminalService(nil, nil)
	raw := svc.RepairCodexSessionVisibility(`{`)
	var response struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatalf("decode response: %v\n%s", err, raw)
	}
	if response.Success {
		t.Fatalf("invalid request succeeded: %s", raw)
	}
	if response.Error == "" {
		t.Fatalf("missing error in response: %s", raw)
	}
}

func TestTerminalServiceGetAllCodexSessionsJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeFile(t, filepath.Join(home, ".codex", "sessions", "2026", "06", "27", "rollout-2026-06-27T10-00-00-thread-1.jsonl"), `{"type":"session_meta","payload":{"cwd":"/tmp/project-a"}}`+"\n")

	svc := NewTerminalService(nil, nil)
	raw := svc.GetAllCodexSessions()
	var response struct {
		Success  bool `json:"success"`
		Sessions []struct {
			SessionID  string `json:"sessionId"`
			ProjectDir string `json:"projectDir"`
		} `json:"sessions"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatalf("decode response: %v\n%s", err, raw)
	}
	if !response.Success {
		t.Fatalf("get sessions failed: %s", response.Message)
	}
	if len(response.Sessions) != 1 || response.Sessions[0].SessionID != "thread-1" || response.Sessions[0].ProjectDir != "/tmp/project-a" {
		t.Fatalf("unexpected response: %#v", response.Sessions)
	}
}

func terminalCreateVisibilityDB(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE threads (
		id TEXT PRIMARY KEY,
		rollout_path TEXT,
		model_provider TEXT,
		has_user_event INTEGER,
		first_user_message TEXT,
		thread_source TEXT
	)`)
	if err != nil {
		t.Fatal(err)
	}
}

func terminalInsertVisibilityThread(t *testing.T, path, id, rolloutPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(
		"INSERT INTO threads (id, rollout_path, model_provider, has_user_event, first_user_message, thread_source) VALUES (?1, ?2, 'old', 0, 'hello', '')",
		id,
		rolloutPath,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func terminalRel(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}
