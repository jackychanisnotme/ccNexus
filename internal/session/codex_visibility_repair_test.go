package session

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestCodexVisibilityQuickRepairsOfficialStateDBOnly(t *testing.T) {
	home, dataDir := setupCodexVisibilityHome(t, `model_provider = "AINexus"`+"\n")
	_ = home
	officialDB := filepath.Join(dataDir, "sqlite", "state_5.sqlite")
	unrelatedDB := filepath.Join(dataDir, "sqlite", "codex-dev.db")
	rolloutPath := filepath.Join(dataDir, "sessions", "2026", "06", "17", "rollout-thread-1.jsonl")
	rolloutRel := mustRel(t, dataDir, rolloutPath)

	createVisibilityDB(t, officialDB)
	insertVisibilityThread(t, officialDB, "thread-1", rolloutRel, "old", 0, "hello", "")
	createVisibilityDB(t, unrelatedDB)
	insertVisibilityThread(t, unrelatedDB, "thread-1", rolloutRel, "old", 0, "hello", "")
	writeFile(t, rolloutPath,
		`{"type":"session_meta","payload":{"id":"thread-1","model_provider":"old"}}`+"\n"+
			`{"type":"session_meta","payload":{"id":"thread-1","model_provider":"old-later"}}`+"\n")

	summary, err := RepairCodexSessionVisibility(CodexVisibilityRepairRequest{Mode: CodexVisibilityRepairModeQuick})
	if err != nil {
		t.Fatalf("repair visibility: %v", err)
	}
	if summary.TargetProvider != "AINexus" {
		t.Fatalf("target provider = %q, want AINexus", summary.TargetProvider)
	}
	if summary.UpdatedSqliteRowCount != 1 {
		t.Fatalf("updated sqlite rows = %d, want 1", summary.UpdatedSqliteRowCount)
	}
	if summary.ChangedRolloutFileCount != 1 {
		t.Fatalf("changed rollout files = %d, want 1", summary.ChangedRolloutFileCount)
	}
	if summary.BackupDir == "" {
		t.Fatal("expected backup dir")
	}
	if _, err := os.Stat(summary.BackupDir); err != nil {
		t.Fatalf("backup dir missing: %v", err)
	}

	provider, hasUserEvent, threadSource := readVisibilityThread(t, officialDB, "thread-1")
	if provider != "AINexus" || hasUserEvent != 1 || threadSource != "user" {
		t.Fatalf("official row = (%q, %d, %q), want (AINexus, 1, user)", provider, hasUserEvent, threadSource)
	}
	unrelatedProvider, unrelatedHasUserEvent, unrelatedSource := readVisibilityThread(t, unrelatedDB, "thread-1")
	if unrelatedProvider != "old" || unrelatedHasUserEvent != 0 || unrelatedSource != "" {
		t.Fatalf("unrelated row changed = (%q, %d, %q)", unrelatedProvider, unrelatedHasUserEvent, unrelatedSource)
	}
	rollout := readFile(t, rolloutPath)
	if !strings.Contains(rollout, `"model_provider":"AINexus"`) {
		t.Fatalf("quick repair did not update first rollout meta: %s", rollout)
	}
	if !strings.Contains(rollout, `"model_provider":"old-later"`) {
		t.Fatalf("quick repair should keep later rollout meta unchanged: %s", rollout)
	}
}

func TestCodexVisibilityDeepRepairsAllReferencedRolloutSessionMeta(t *testing.T) {
	_, dataDir := setupCodexVisibilityHome(t, `model_provider = "relay"`+"\n")
	officialDB := filepath.Join(dataDir, "sqlite", "state_5.sqlite")
	rolloutPath := filepath.Join(dataDir, "sessions", "2026", "06", "17", "rollout-thread-1.jsonl")
	rolloutRel := mustRel(t, dataDir, rolloutPath)

	createVisibilityDB(t, officialDB)
	insertVisibilityThread(t, officialDB, "thread-1", rolloutRel, "old", 0, "hello", "")
	writeFile(t, rolloutPath,
		`{"type":"session_meta","payload":{"id":"thread-1","model_provider":"old"}}`+"\n"+
			`{"type":"event_msg","payload":{"type":"user_message","message":"hello"}}`+"\n"+
			`{"type":"session_meta","payload":{"id":"thread-1","model_provider":"old-later"}}`+"\n")

	summary, err := RepairCodexSessionVisibility(CodexVisibilityRepairRequest{Mode: CodexVisibilityRepairModeDeep})
	if err != nil {
		t.Fatalf("repair visibility: %v", err)
	}
	if summary.ChangedRolloutFileCount != 1 {
		t.Fatalf("changed rollout files = %d, want 1", summary.ChangedRolloutFileCount)
	}
	rollout := readFile(t, rolloutPath)
	if strings.Contains(rollout, `"model_provider":"old"`) || strings.Contains(rollout, `"model_provider":"old-later"`) {
		t.Fatalf("deep repair left old providers in rollout: %s", rollout)
	}
	if got := strings.Count(rollout, `"model_provider":"relay"`); got != 2 {
		t.Fatalf("deep repair provider count = %d, want 2 in %s", got, rollout)
	}
}

func TestCodexVisibilityQuickRepairHandlesLargeRolloutLines(t *testing.T) {
	_, dataDir := setupCodexVisibilityHome(t, `model_provider = "AINexus"`+"\n")
	officialDB := filepath.Join(dataDir, "sqlite", "state_5.sqlite")
	rolloutPath := filepath.Join(dataDir, "sessions", "2026", "06", "17", "rollout-thread-1.jsonl")
	rolloutRel := mustRel(t, dataDir, rolloutPath)
	largeMessage := strings.Repeat("x", 2*1024*1024)

	createVisibilityDB(t, officialDB)
	insertVisibilityThread(t, officialDB, "thread-1", rolloutRel, "old", 0, "hello", "")
	writeFile(t, rolloutPath,
		`{"type":"session_meta","payload":{"id":"thread-1","model_provider":"old"}}`+"\n"+
			`{"type":"event_msg","payload":{"type":"user_message","message":"`+largeMessage+`"}}`+"\n")

	summary, err := RepairCodexSessionVisibility(CodexVisibilityRepairRequest{Mode: CodexVisibilityRepairModeQuick})
	if err != nil {
		t.Fatalf("repair visibility with large rollout line: %v", err)
	}
	if summary.ChangedRolloutFileCount != 1 {
		t.Fatalf("changed rollout files = %d, want 1", summary.ChangedRolloutFileCount)
	}
	rollout := readFile(t, rolloutPath)
	if !strings.Contains(rollout, `"model_provider":"AINexus"`) {
		t.Fatalf("quick repair did not update rollout meta")
	}
	if !strings.Contains(rollout, largeMessage) {
		t.Fatalf("quick repair did not preserve large rollout line")
	}
}

func TestCodexVisibilityRepairSelectedSessionsOnly(t *testing.T) {
	_, dataDir := setupCodexVisibilityHome(t, `model_provider = "AINexus"`+"\n")
	officialDB := filepath.Join(dataDir, "sqlite", "state_5.sqlite")
	firstRollout := filepath.Join(dataDir, "sessions", "2026", "06", "17", "rollout-thread-1.jsonl")
	secondRollout := filepath.Join(dataDir, "sessions", "2026", "06", "17", "rollout-thread-2.jsonl")

	createVisibilityDB(t, officialDB)
	insertVisibilityThread(t, officialDB, "thread-1", mustRel(t, dataDir, firstRollout), "old", 0, "hello", "")
	insertVisibilityThread(t, officialDB, "thread-2", mustRel(t, dataDir, secondRollout), "old", 0, "hello", "")
	writeFile(t, firstRollout, `{"type":"session_meta","payload":{"id":"thread-1","model_provider":"old"}}`+"\n")
	writeFile(t, secondRollout, `{"type":"session_meta","payload":{"id":"thread-2","model_provider":"old"}}`+"\n")

	summary, err := RepairCodexSessionVisibility(CodexVisibilityRepairRequest{
		Mode:         CodexVisibilityRepairModeQuick,
		SessionScope: CodexVisibilityRepairSessionScopeSelected,
		SessionIDs:   []string{"thread-1"},
	})
	if err != nil {
		t.Fatalf("repair visibility: %v", err)
	}
	if summary.UpdatedSqliteRowCount != 1 || summary.ChangedRolloutFileCount != 1 {
		t.Fatalf("summary rows/files = %d/%d, want 1/1", summary.UpdatedSqliteRowCount, summary.ChangedRolloutFileCount)
	}
	firstProvider, _, _ := readVisibilityThread(t, officialDB, "thread-1")
	secondProvider, _, _ := readVisibilityThread(t, officialDB, "thread-2")
	if firstProvider != "AINexus" || secondProvider != "old" {
		t.Fatalf("providers = %q/%q, want AINexus/old", firstProvider, secondProvider)
	}
	if strings.Contains(readFile(t, secondRollout), "AINexus") {
		t.Fatalf("selected repair changed unselected rollout: %s", readFile(t, secondRollout))
	}
}

func TestCodexVisibilityRepairMissingDatabaseAndThreadsTableAreNoop(t *testing.T) {
	_, dataDir := setupCodexVisibilityHome(t, "")
	noThreadsDB := filepath.Join(dataDir, "sqlite", "state_5.sqlite")
	if err := os.MkdirAll(filepath.Dir(noThreadsDB), 0755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", noThreadsDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE unrelated (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	summary, err := RepairCodexSessionVisibility(CodexVisibilityRepairRequest{})
	if err != nil {
		t.Fatalf("repair visibility: %v", err)
	}
	if summary.TargetProvider != "openai" {
		t.Fatalf("target provider = %q, want openai", summary.TargetProvider)
	}
	if summary.UpdatedSqliteRowCount != 0 || summary.ChangedRolloutFileCount != 0 {
		t.Fatalf("noop changed rows/files = %d/%d", summary.UpdatedSqliteRowCount, summary.ChangedRolloutFileCount)
	}
	if summary.BackupDir != "" {
		t.Fatalf("noop backup dir = %q, want empty", summary.BackupDir)
	}
}

func TestCodexVisibilityRepairRollsBackOnRolloutWriteFailure(t *testing.T) {
	_, dataDir := setupCodexVisibilityHome(t, `model_provider = "AINexus"`+"\n")
	officialDB := filepath.Join(dataDir, "sqlite", "state_5.sqlite")
	rolloutPath := filepath.Join(dataDir, "sessions", "2026", "06", "17", "rollout-thread-1.jsonl")
	rolloutRel := mustRel(t, dataDir, rolloutPath)
	originalRollout := `{"type":"session_meta","payload":{"id":"thread-1","model_provider":"old"}}` + "\n"

	createVisibilityDB(t, officialDB)
	insertVisibilityThread(t, officialDB, "thread-1", rolloutRel, "old", 0, "hello", "")
	writeFile(t, rolloutPath, originalRollout)
	if err := os.Chmod(rolloutPath, 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(rolloutPath, 0600) })

	_, err := RepairCodexSessionVisibility(CodexVisibilityRepairRequest{Mode: CodexVisibilityRepairModeQuick})
	if err == nil {
		t.Fatal("expected repair failure")
	}
	provider, hasUserEvent, threadSource := readVisibilityThread(t, officialDB, "thread-1")
	if provider != "old" || hasUserEvent != 0 || threadSource != "" {
		t.Fatalf("sqlite was not rolled back: (%q, %d, %q)", provider, hasUserEvent, threadSource)
	}
	if got := readFile(t, rolloutPath); got != originalRollout {
		t.Fatalf("rollout was not preserved: %q", got)
	}
}

func setupCodexVisibilityHome(t *testing.T, config string) (string, string) {
	t.Helper()
	home := t.TempDir()
	dataDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if config != "" {
		writeFile(t, filepath.Join(dataDir, "config.toml"), config)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home, dataDir
}

func createVisibilityDB(t *testing.T, path string) {
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

func insertVisibilityThread(t *testing.T, path, id, rolloutPath, provider string, hasUserEvent int, firstUserMessage, threadSource string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(
		"INSERT INTO threads (id, rollout_path, model_provider, has_user_event, first_user_message, thread_source) VALUES (?1, ?2, ?3, ?4, ?5, ?6)",
		id,
		rolloutPath,
		provider,
		hasUserEvent,
		firstUserMessage,
		threadSource,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func readVisibilityThread(t *testing.T, path, id string) (string, int, string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var provider string
	var hasUserEvent int
	var threadSource string
	err = db.QueryRow(
		"SELECT COALESCE(model_provider, ''), COALESCE(has_user_event, 0), COALESCE(thread_source, '') FROM threads WHERE id = ?1",
		id,
	).Scan(&provider, &hasUserEvent, &threadSource)
	if err != nil {
		t.Fatal(err)
	}
	return provider, hasUserEvent, threadSource
}

func mustRel(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
