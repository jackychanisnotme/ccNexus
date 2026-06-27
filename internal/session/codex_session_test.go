package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetAllCodexSessionsReturnsSessionsAcrossProjects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	writeCodexSessionTestFile(t, home, filepath.Join("2026", "06", "27", "rollout-2026-06-27T10-00-00-alpha.jsonl"), "/tmp/project-a", "first alpha message", time.Unix(100, 0))
	writeCodexSessionTestFile(t, home, filepath.Join("2026", "06", "27", "rollout-2026-06-27T11-00-00-beta.jsonl"), "/tmp/project-b", "first beta message", time.Unix(200, 0))

	sessions, err := GetAllCodexSessions()
	if err != nil {
		t.Fatalf("get all codex sessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d: %#v", len(sessions), sessions)
	}
	if sessions[0].SessionID != "beta" || sessions[0].ProjectDir != "/tmp/project-b" {
		t.Fatalf("expected newest beta project first, got %#v", sessions[0])
	}
	if sessions[1].SessionID != "alpha" || sessions[1].ProjectDir != "/tmp/project-a" {
		t.Fatalf("expected alpha project second, got %#v", sessions[1])
	}
}

func TestGetAllCodexSessionsMissingDirectoryReturnsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	sessions, err := GetAllCodexSessions()
	if err != nil {
		t.Fatalf("get all codex sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected empty sessions, got %#v", sessions)
	}
}

func writeCodexSessionTestFile(t *testing.T, home, relativePath, cwd, message string, modTime time.Time) {
	t.Helper()
	path := filepath.Join(home, ".codex", "sessions", relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	content := `{"type":"session_meta","payload":{"cwd":"` + cwd + `"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"user_message","message":"` + message + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}
