package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrateDeepSeekThinkingDefaultRunsOnce(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ccnexus.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE endpoints (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			api_url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			auth_mode TEXT NOT NULL DEFAULT 'api_key',
			enabled BOOLEAN DEFAULT TRUE,
			transformer TEXT DEFAULT 'claude',
			model TEXT,
			thinking TEXT DEFAULT 'off',
			force_stream BOOLEAN DEFAULT FALSE,
			remark TEXT,
			sort_order INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE app_config (
			key TEXT PRIMARY KEY,
			value TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO endpoints (name, api_url, api_key, auth_mode, enabled, transformer, model, thinking, remark)
		VALUES
			('deepseek-old', 'https://api.deepseek.com', 'key', 'api_key', TRUE, 'deepseek', 'deepseek-chat', 'off', ''),
			('openai-old', 'https://api.openai.com', 'key', 'api_key', TRUE, 'openai', 'gpt-4', 'off', ''),
			('deepseek-high', 'https://api.deepseek.com', 'key', 'api_key', TRUE, 'deepseek', 'deepseek-chat', 'high', '');
	`)
	if err != nil {
		t.Fatalf("seed database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed database: %v", err)
	}

	store, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	endpoints, err := store.GetEndpoints()
	if err != nil {
		t.Fatalf("get endpoints: %v", err)
	}
	thinkingByName := map[string]string{}
	for _, ep := range endpoints {
		thinkingByName[ep.Name] = ep.Thinking
	}
	if got := thinkingByName["deepseek-old"]; got != "" {
		t.Fatalf("expected old DeepSeek off to migrate to provider default, got %q", got)
	}
	if got := thinkingByName["openai-old"]; got != "off" {
		t.Fatalf("expected OpenAI off to stay off, got %q", got)
	}
	if got := thinkingByName["deepseek-high"]; got != "high" {
		t.Fatalf("expected DeepSeek high to stay high, got %q", got)
	}
	if marker, err := store.GetConfig(deepSeekThinkingDefaultMigrationKey); err != nil || marker != "done" {
		t.Fatalf("expected migration marker done, got %q err=%v", marker, err)
	}

	if _, err := store.db.Exec(`UPDATE endpoints SET thinking='off' WHERE name='deepseek-old'`); err != nil {
		t.Fatalf("set explicit off after migration: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close storage: %v", err)
	}

	store, err = NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("reopen storage: %v", err)
	}
	defer store.Close()
	endpoints, err = store.GetEndpoints()
	if err != nil {
		t.Fatalf("get endpoints after reopen: %v", err)
	}
	for _, ep := range endpoints {
		if ep.Name == "deepseek-old" && ep.Thinking != "off" {
			t.Fatalf("expected explicit DeepSeek off to survive after marker, got %q", ep.Thinking)
		}
	}
}

func TestMigrateEndpointProxyURLRunsOnce(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ccnexus.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE endpoints (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			api_url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			auth_mode TEXT NOT NULL DEFAULT 'api_key',
			enabled BOOLEAN DEFAULT TRUE,
			transformer TEXT DEFAULT 'claude',
			model TEXT,
			thinking TEXT DEFAULT '',
			force_stream BOOLEAN DEFAULT FALSE,
			remark TEXT,
			sort_order INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE app_config (
			key TEXT PRIMARY KEY,
			value TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO endpoints (name, api_url, api_key, auth_mode, enabled, transformer, model, thinking, remark)
		VALUES ('Primary', 'https://api.example.com', 'key', 'api_key', TRUE, 'claude', '', '', '');
	`)
	if err != nil {
		t.Fatalf("seed database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed database: %v", err)
	}

	store, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	endpoints, err := store.GetEndpoints()
	if err != nil {
		t.Fatalf("get endpoints: %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected one endpoint, got %d", len(endpoints))
	}
	if endpoints[0].ProxyURL != "" {
		t.Fatalf("expected empty proxy URL after migration, got %q", endpoints[0].ProxyURL)
	}

	endpoints[0].ProxyURL = "http://127.0.0.1:7890"
	if err := store.UpdateEndpoint(&endpoints[0]); err != nil {
		t.Fatalf("update endpoint proxy url: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated storage: %v", err)
	}

	store, err = NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("reopen storage: %v", err)
	}
	defer store.Close()
	endpoints, err = store.GetEndpoints()
	if err != nil {
		t.Fatalf("reload endpoints: %v", err)
	}
	if got := endpoints[0].ProxyURL; got != "http://127.0.0.1:7890" {
		t.Fatalf("expected proxy URL to persist, got %q", got)
	}
}

func TestDailyStatsClientIPDimensionAndFilters(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ccnexus.db")
	store, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	date := "2026-06-13"
	records := []DailyStat{
		{EndpointName: "Primary", Date: date, Requests: 1, InputTokens: 10, OutputTokens: 20, DeviceID: "device-a", ClientIP: "192.168.1.10"},
		{EndpointName: "Primary", Date: date, Requests: 2, Errors: 1, InputTokens: 30, OutputTokens: 40, DeviceID: "device-a", ClientIP: "192.168.1.20"},
		{EndpointName: "Secondary", Date: date, Requests: 3, InputTokens: 50, OutputTokens: 60, DeviceID: "device-a", ClientIP: "10.0.0.5"},
	}
	for i := range records {
		if err := store.RecordDailyStat(&records[i]); err != nil {
			t.Fatalf("record stat %d: %v", i, err)
		}
	}

	all, err := store.GetPeriodStatsAggregatedFiltered(date, date, StatsFilter{})
	if err != nil {
		t.Fatalf("get unfiltered stats: %v", err)
	}
	if got := all["Primary"].Requests; got != 3 {
		t.Fatalf("unfiltered Primary requests = %d, want 3", got)
	}
	if got := all["Secondary"].InputTokens; got != int64(50) {
		t.Fatalf("unfiltered Secondary input tokens = %d, want 50", got)
	}

	byEndpoint, err := store.GetPeriodStatsAggregatedFiltered(date, date, StatsFilter{EndpointName: "Primary"})
	if err != nil {
		t.Fatalf("get endpoint-filtered stats: %v", err)
	}
	if len(byEndpoint) != 1 || byEndpoint["Primary"].Requests != 3 {
		t.Fatalf("endpoint filter returned %#v, want only Primary with 3 requests", byEndpoint)
	}

	byIP, err := store.GetPeriodStatsAggregatedFiltered(date, date, StatsFilter{ClientIP: "192.168.1.10"})
	if err != nil {
		t.Fatalf("get ip-filtered stats: %v", err)
	}
	if len(byIP) != 1 || byIP["Primary"].Requests != 1 || byIP["Primary"].InputTokens != 10 {
		t.Fatalf("client IP filter returned %#v, want Primary with first IP only", byIP)
	}

	byIPQuery, err := store.GetPeriodStatsAggregatedFiltered(date, date, StatsFilter{ClientIPQuery: "1.20"})
	if err != nil {
		t.Fatalf("get fuzzy ip-filtered stats: %v", err)
	}
	if len(byIPQuery) != 1 || byIPQuery["Primary"].Requests != 2 || byIPQuery["Primary"].Errors != 1 {
		t.Fatalf("client IP query returned %#v, want Primary with second IP only", byIPQuery)
	}
}

func TestDailyStatsMigrationAddsClientIPAndPreservesStatsAfterEndpointDelete(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ccnexus.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE endpoints (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			api_url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			auth_mode TEXT NOT NULL DEFAULT 'api_key',
			enabled BOOLEAN DEFAULT TRUE,
			transformer TEXT DEFAULT 'claude',
			model TEXT,
			thinking TEXT DEFAULT '',
			force_stream BOOLEAN DEFAULT FALSE,
			proxy_url TEXT DEFAULT '',
			remark TEXT,
			sort_order INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE daily_stats (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			endpoint_name TEXT NOT NULL,
			date TEXT NOT NULL,
			requests INTEGER DEFAULT 0,
			errors INTEGER DEFAULT 0,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			device_id TEXT DEFAULT 'default',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(endpoint_name, date, device_id)
		);
		CREATE TABLE app_config (
			key TEXT PRIMARY KEY,
			value TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO endpoints (name, api_url, api_key, auth_mode, enabled, transformer, model, thinking, remark)
		VALUES ('DeletedLater', 'https://api.example.com', 'key', 'api_key', TRUE, 'openai', 'gpt-test', '', '');
		INSERT INTO daily_stats (endpoint_name, date, requests, errors, input_tokens, output_tokens, device_id)
		VALUES ('DeletedLater', '2026-06-13', 4, 1, 80, 90, 'device-a');
	`)
	if err != nil {
		t.Fatalf("seed old daily_stats schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed database: %v", err)
	}

	store, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("open migrated storage: %v", err)
	}
	defer store.Close()

	stats, err := store.GetPeriodStatsAggregatedFiltered("2026-06-13", "2026-06-13", StatsFilter{ClientIP: "unknown"})
	if err != nil {
		t.Fatalf("get migrated unknown-ip stats: %v", err)
	}
	if got := stats["DeletedLater"]; got == nil || got.Requests != 4 || got.InputTokens != 80 {
		t.Fatalf("migrated stats = %#v, want DeletedLater totals under unknown IP", stats)
	}

	if err := store.DeleteEndpoint("DeletedLater"); err != nil {
		t.Fatalf("delete endpoint: %v", err)
	}
	stats, err = store.GetPeriodStatsAggregatedFiltered("2026-06-13", "2026-06-13", StatsFilter{EndpointName: "DeletedLater"})
	if err != nil {
		t.Fatalf("get deleted endpoint stats: %v", err)
	}
	if got := stats["DeletedLater"]; got == nil || got.Requests != 4 {
		t.Fatalf("deleted endpoint stats = %#v, want preserved stats", stats)
	}

	options, err := store.GetStatsFilterOptions([]string{"CurrentOnly"})
	if err != nil {
		t.Fatalf("get filter options: %v", err)
	}
	if !hasStatsEndpointOption(options.Endpoints, "DeletedLater", true) {
		t.Fatalf("expected deleted endpoint option, got %#v", options.Endpoints)
	}
	if !hasStatsEndpointOption(options.Endpoints, "CurrentOnly", false) {
		t.Fatalf("expected current endpoint option, got %#v", options.Endpoints)
	}
	if len(options.ClientIPs) != 1 || options.ClientIPs[0] != "unknown" {
		t.Fatalf("client IP options = %#v, want [unknown]", options.ClientIPs)
	}
}

func TestMergeDailyStatsFromOldBackupUsesUnknownClientIP(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "local.db")
	store, err := NewSQLiteStorage(localPath)
	if err != nil {
		t.Fatalf("open local storage: %v", err)
	}
	defer store.Close()

	backupPath := filepath.Join(t.TempDir(), "backup.db")
	db, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatalf("open backup sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE endpoints (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			api_url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			auth_mode TEXT NOT NULL DEFAULT 'api_key',
			enabled BOOLEAN DEFAULT TRUE,
			transformer TEXT DEFAULT 'claude',
			model TEXT,
			thinking TEXT DEFAULT '',
			force_stream BOOLEAN DEFAULT FALSE,
			remark TEXT,
			sort_order INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE daily_stats (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			endpoint_name TEXT NOT NULL,
			date TEXT NOT NULL,
			requests INTEGER DEFAULT 0,
			errors INTEGER DEFAULT 0,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			device_id TEXT DEFAULT 'backup-device',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(endpoint_name, date, device_id)
		);
		CREATE TABLE app_config (
			key TEXT PRIMARY KEY,
			value TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO endpoints (name, api_url, api_key, auth_mode, enabled, transformer, model, thinking, remark)
		VALUES ('BackupEndpoint', 'https://api.example.com', 'key', 'api_key', TRUE, 'openai', 'gpt-test', '', '');
		INSERT INTO daily_stats (endpoint_name, date, requests, errors, input_tokens, output_tokens, device_id)
		VALUES ('BackupEndpoint', '2026-06-13', 7, 2, 100, 200, 'backup-device');
	`)
	if err != nil {
		t.Fatalf("seed old backup: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close backup database: %v", err)
	}

	if err := store.MergeFromBackup(backupPath, MergeStrategyKeepLocal); err != nil {
		t.Fatalf("merge old backup: %v", err)
	}
	stats, err := store.GetPeriodStatsAggregatedFiltered("2026-06-13", "2026-06-13", StatsFilter{ClientIP: "unknown"})
	if err != nil {
		t.Fatalf("get merged stats: %v", err)
	}
	if got := stats["BackupEndpoint"]; got == nil || got.Requests != 7 || got.Errors != 2 {
		t.Fatalf("merged stats = %#v, want old backup stats under unknown IP", stats)
	}
}

func hasStatsEndpointOption(options []StatsEndpointFilterOption, name string, deleted bool) bool {
	for _, option := range options {
		if option.Name == name && option.Deleted == deleted {
			return true
		}
	}
	return false
}

func TestEndpointRuntimeStatusPersistsAcrossReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ccnexus.db")
	store, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}

	successAt := time.Date(2026, 5, 8, 9, 10, 11, 0, time.UTC)
	attemptAt := successAt.Add(-time.Second)
	status, err := store.UpsertEndpointRuntimeStatus("Primary", EndpointRuntimeStatusPatch{
		LastSuccessAt: &successAt,
		LastAttemptAt: &attemptAt,
	})
	if err != nil {
		t.Fatalf("upsert success status: %v", err)
	}
	if status.LastSuccessAt == nil || !status.LastSuccessAt.Equal(successAt) {
		t.Fatalf("expected success time %s, got %#v", successAt, status.LastSuccessAt)
	}

	failureAt := successAt.Add(time.Minute)
	reason := "upstream_5xx"
	statusCode := 500
	status, err = store.UpsertEndpointRuntimeStatus("Primary", EndpointRuntimeStatusPatch{
		LastFailureAt:         &failureAt,
		LastFailureReason:     &reason,
		LastFailureStatusCode: &statusCode,
	})
	if err != nil {
		t.Fatalf("upsert failure status: %v", err)
	}
	if status.LastSuccessAt == nil || !status.LastSuccessAt.Equal(successAt) {
		t.Fatalf("expected success time to be preserved, got %#v", status.LastSuccessAt)
	}
	if status.LastFailureAt == nil || !status.LastFailureAt.Equal(failureAt) {
		t.Fatalf("expected failure time %s, got %#v", failureAt, status.LastFailureAt)
	}
	if status.LastFailureReason != reason {
		t.Fatalf("expected failure reason %q, got %q", reason, status.LastFailureReason)
	}
	if status.LastFailureStatusCode != statusCode {
		t.Fatalf("expected failure status code %d, got %d", statusCode, status.LastFailureStatusCode)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close storage: %v", err)
	}

	store, err = NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("reopen storage: %v", err)
	}
	defer store.Close()

	statuses, err := store.GetEndpointRuntimeStatuses()
	if err != nil {
		t.Fatalf("get runtime statuses: %v", err)
	}
	status = statuses["Primary"]
	if status == nil {
		t.Fatalf("expected Primary runtime status after reopen")
	}
	if status.LastSuccessAt == nil || !status.LastSuccessAt.Equal(successAt) {
		t.Fatalf("expected persisted success time %s, got %#v", successAt, status.LastSuccessAt)
	}
	if status.LastFailureAt == nil || !status.LastFailureAt.Equal(failureAt) {
		t.Fatalf("expected persisted failure time %s, got %#v", failureAt, status.LastFailureAt)
	}
	if status.LastFailureReason != reason {
		t.Fatalf("expected persisted failure reason %q, got %q", reason, status.LastFailureReason)
	}
	if status.LastFailureStatusCode != statusCode {
		t.Fatalf("expected persisted failure status code %d, got %d", statusCode, status.LastFailureStatusCode)
	}

	nonHTTPFailureAt := failureAt.Add(time.Minute)
	nonHTTPReason := "transient_network_error"
	emptyStatusCode := 0
	status, err = store.UpsertEndpointRuntimeStatus("Primary", EndpointRuntimeStatusPatch{
		LastFailureAt:         &nonHTTPFailureAt,
		LastFailureReason:     &nonHTTPReason,
		LastFailureStatusCode: &emptyStatusCode,
	})
	if err != nil {
		t.Fatalf("upsert non-http failure status: %v", err)
	}
	if status.LastFailureStatusCode != 0 {
		t.Fatalf("expected non-http failure to clear status code, got %d", status.LastFailureStatusCode)
	}
}

func TestMigrateEndpointRuntimeFailureStatusCode(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ccnexus.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE endpoint_runtime_status (
			endpoint_name TEXT PRIMARY KEY,
			last_success_at DATETIME,
			last_failure_at DATETIME,
			last_failure_reason TEXT,
			last_attempt_at DATETIME,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO endpoint_runtime_status (endpoint_name, last_failure_reason)
		VALUES ('Primary', 'rate_limited');
	`)
	if err != nil {
		t.Fatalf("create old runtime status schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close old storage: %v", err)
	}

	store, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("open migrated storage: %v", err)
	}
	defer store.Close()

	statuses, err := store.GetEndpointRuntimeStatuses()
	if err != nil {
		t.Fatalf("get migrated runtime statuses: %v", err)
	}
	status := statuses["Primary"]
	if status == nil {
		t.Fatalf("expected migrated Primary runtime status")
	}
	if status.LastFailureStatusCode != 0 {
		t.Fatalf("expected migrated status code default 0, got %d", status.LastFailureStatusCode)
	}

	failureAt := time.Date(2026, 5, 8, 10, 11, 12, 0, time.UTC)
	reason := "rate_limited"
	statusCode := 429
	status, err = store.UpsertEndpointRuntimeStatus("Primary", EndpointRuntimeStatusPatch{
		LastFailureAt:         &failureAt,
		LastFailureReason:     &reason,
		LastFailureStatusCode: &statusCode,
	})
	if err != nil {
		t.Fatalf("upsert migrated failure status: %v", err)
	}
	if status.LastFailureStatusCode != statusCode {
		t.Fatalf("expected migrated status code %d, got %d", statusCode, status.LastFailureStatusCode)
	}
}
