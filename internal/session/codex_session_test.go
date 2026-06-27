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

	writeCodexSessionTestFile(t, home, "sessions", filepath.Join("2026", "06", "27", "rollout-2026-06-27T10-00-00-alpha.jsonl"), "/tmp/project-a", "first alpha message", time.Unix(100, 0))
	writeCodexSessionTestFile(t, home, "sessions", filepath.Join("2026", "06", "27", "rollout-2026-06-27T11-00-00-beta.jsonl"), "/tmp/project-b", "first beta message", time.Unix(200, 0))

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

func TestGetAllCodexSessionsIncludesArchivedSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	writeCodexSessionTestFile(t, home, "sessions", filepath.Join("2026", "06", "27", "rollout-2026-06-27T10-00-00-live.jsonl"), "/tmp/live-project", "live message", time.Unix(100, 0))
	writeCodexSessionTestFile(t, home, "archived_sessions", filepath.Join("2026", "06", "26", "rollout-2026-06-26T09-00-00-archived.jsonl"), "/tmp/archived-project", "archived message", time.Unix(200, 0))
	writeCodexSessionTestFile(t, home, "thread-move-backups", filepath.Join("2026", "06", "25", "rollout-2026-06-25T09-00-00-backup.jsonl"), "/tmp/backup-project", "backup message", time.Unix(300, 0))

	sessions, err := GetAllCodexSessions()
	if err != nil {
		t.Fatalf("get all codex sessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 live/archived sessions, got %d: %#v", len(sessions), sessions)
	}
	if sessions[0].SessionID != "archived" || sessions[0].ProjectDir != "/tmp/archived-project" {
		t.Fatalf("expected archived session first by mtime, got %#v", sessions[0])
	}
	if sessions[1].SessionID != "live" || sessions[1].ProjectDir != "/tmp/live-project" {
		t.Fatalf("expected live session second, got %#v", sessions[1])
	}
}

func TestGetAllCodexSessionsPrefersLiveSessionOverArchivedDuplicate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	writeCodexSessionTestFile(t, home, "archived_sessions", filepath.Join("2026", "06", "26", "rollout-2026-06-26T09-00-00-duplicate.jsonl"), "/tmp/archived-project", "archived message", time.Unix(300, 0))
	writeCodexSessionTestFile(t, home, "sessions", filepath.Join("2026", "06", "27", "rollout-2026-06-27T10-00-00-duplicate.jsonl"), "/tmp/live-project", "live message", time.Unix(100, 0))

	sessions, err := GetAllCodexSessions()
	if err != nil {
		t.Fatalf("get all codex sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected duplicate session to be deduped, got %d: %#v", len(sessions), sessions)
	}
	if sessions[0].ProjectDir != "/tmp/live-project" || sessions[0].Summary != "live message" {
		t.Fatalf("expected live session to win duplicate, got %#v", sessions[0])
	}
}

func TestGetCodexSessionDataFindsArchivedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	writeCodexSessionTestFile(t, home, "archived_sessions", filepath.Join("2026", "06", "26", "rollout-2026-06-26T09-00-00-archived-read.jsonl"), "/tmp/archived-project", "archived read message", time.Unix(100, 0))

	messages, err := GetCodexSessionData("archived-read")
	if err != nil {
		t.Fatalf("get archived codex session data: %v", err)
	}
	if len(messages) != 1 || messages[0].Type != "user" || messages[0].Content != "archived read message" {
		t.Fatalf("unexpected archived messages: %#v", messages)
	}
}

func TestDeleteCodexSessionFindsArchivedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	archivedPath := writeCodexSessionTestFile(t, home, "archived_sessions", filepath.Join("2026", "06", "26", "rollout-2026-06-26T09-00-00-archived-delete.jsonl"), "/tmp/archived-project", "archived delete message", time.Unix(100, 0))

	if err := DeleteCodexSession("archived-delete"); err != nil {
		t.Fatalf("delete archived codex session: %v", err)
	}
	if _, err := os.Stat(archivedPath); !os.IsNotExist(err) {
		t.Fatalf("expected archived session to be removed, stat err=%v", err)
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

func writeCodexSessionTestFile(t *testing.T, home, rootName, relativePath, cwd, message string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(home, ".codex", rootName, relativePath)
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
	return path
}
