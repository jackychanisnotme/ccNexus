package onlinelicense

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	store := &SQLiteStore{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteStore) init() error {
	schema := `
	PRAGMA journal_mode=WAL;
	PRAGMA busy_timeout=5000;

	CREATE TABLE IF NOT EXISTS license_cards (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		card_hash TEXT UNIQUE NOT NULL,
		plan TEXT NOT NULL,
		days INTEGER NOT NULL,
		max_devices INTEGER NOT NULL DEFAULT 1,
		status TEXT NOT NULL DEFAULT 'active',
		customer TEXT,
		remark TEXT,
		created_at DATETIME NOT NULL,
		disabled_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS license_activations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		card_id INTEGER NOT NULL,
		device_id TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'active',
		activated_at DATETIME NOT NULL,
		expires_at DATETIME NOT NULL,
		last_checked_at DATETIME NOT NULL,
		disabled_at DATETIME,
		platform TEXT,
		app_version TEXT,
		ip_address TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(card_id, device_id)
	);

	CREATE TABLE IF NOT EXISTS admin_audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		action TEXT NOT NULL,
		target_type TEXT NOT NULL,
		target_id INTEGER NOT NULL DEFAULT 0,
		detail TEXT,
		created_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS license_device_notes (
		device_id TEXT PRIMARY KEY,
		remark TEXT NOT NULL DEFAULT '',
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS license_device_remote_state (
		device_id TEXT PRIMARY KEY,
		supported INTEGER NOT NULL DEFAULT 0,
		enabled INTEGER NOT NULL DEFAULT 0,
		client_version TEXT,
		capabilities_json TEXT NOT NULL DEFAULT '[]',
		device_public_key TEXT,
		last_heartbeat_at DATETIME,
		last_activation_id INTEGER NOT NULL DEFAULT 0,
		owner_account_id INTEGER NOT NULL DEFAULT 0,
		owner_username TEXT,
		last_snapshot_status TEXT,
		last_snapshot_at DATETIME,
		last_command_status TEXT,
		last_command_updated_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS license_remote_snapshots (
		device_id TEXT PRIMARY KEY,
		snapshot_json TEXT NOT NULL DEFAULT '{}',
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS license_remote_commands (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		device_id TEXT NOT NULL,
		command_type TEXT NOT NULL,
		status TEXT NOT NULL,
		actor_account_id INTEGER NOT NULL DEFAULT 0,
		actor_username TEXT,
		owner_account_id INTEGER NOT NULL DEFAULT 0,
		envelope_json TEXT NOT NULL DEFAULT '{}',
		summary_json TEXT,
		result TEXT,
		result_json TEXT,
		error TEXT,
		expires_at DATETIME,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS license_endpoint_error_windows (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		device_id TEXT NOT NULL,
		activation_id INTEGER NOT NULL DEFAULT 0,
		owner_account_id INTEGER NOT NULL DEFAULT 0,
		platform TEXT,
		app_version TEXT,
		endpoint_name TEXT NOT NULL DEFAULT '',
		endpoint_fingerprint TEXT NOT NULL,
		api_host TEXT,
		api_url_fingerprint TEXT,
		auth_mode TEXT,
		transformer TEXT,
		model TEXT,
		reason TEXT NOT NULL,
		status_code INTEGER NOT NULL DEFAULT 0,
		count INTEGER NOT NULL DEFAULT 0,
		first_at DATETIME NOT NULL,
		last_at DATETIME NOT NULL,
		window_start DATETIME NOT NULL,
		window_end DATETIME NOT NULL,
		sample TEXT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		UNIQUE(device_id, endpoint_fingerprint, reason, status_code, window_start)
	);

	CREATE TABLE IF NOT EXISTS admin_accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		display_name TEXT,
		level INTEGER NOT NULL,
		parent_id INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'active',
		permissions TEXT NOT NULL DEFAULT '',
		created_by INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_license_cards_status ON license_cards(status);
	CREATE INDEX IF NOT EXISTS idx_license_cards_created_at ON license_cards(created_at);
	CREATE INDEX IF NOT EXISTS idx_license_activations_device ON license_activations(device_id);
	CREATE INDEX IF NOT EXISTS idx_license_activations_status ON license_activations(status);
	CREATE INDEX IF NOT EXISTS idx_license_activations_expires ON license_activations(expires_at);
	CREATE INDEX IF NOT EXISTS idx_admin_audit_logs_created_at ON admin_audit_logs(created_at);
	CREATE INDEX IF NOT EXISTS idx_admin_accounts_parent ON admin_accounts(parent_id);
	CREATE INDEX IF NOT EXISTS idx_license_remote_commands_device ON license_remote_commands(device_id, status, created_at);
	CREATE INDEX IF NOT EXISTS idx_license_endpoint_error_windows_device ON license_endpoint_error_windows(device_id, window_start);
	CREATE INDEX IF NOT EXISTS idx_license_endpoint_error_windows_owner ON license_endpoint_error_windows(owner_account_id, window_start);
	CREATE INDEX IF NOT EXISTS idx_license_endpoint_error_windows_reason ON license_endpoint_error_windows(reason, status_code, window_start);

	UPDATE license_activations
	SET expires_at = disabled_at, updated_at = CURRENT_TIMESTAMP
	WHERE status = 'disabled' AND disabled_at IS NOT NULL AND expires_at > disabled_at;
	`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	for _, column := range []struct {
		table      string
		name       string
		definition string
	}{
		{"license_cards", "owner_account_id", "INTEGER NOT NULL DEFAULT 0"},
		{"license_cards", "created_by_account_id", "INTEGER NOT NULL DEFAULT 0"},
		{"admin_audit_logs", "actor_account_id", "INTEGER NOT NULL DEFAULT 0"},
		{"admin_audit_logs", "actor_username", "TEXT"},
		{"admin_audit_logs", "ip_address", "TEXT"},
		{"license_remote_commands", "summary_json", "TEXT"},
		{"license_remote_commands", "result_json", "TEXT"},
		{"license_remote_commands", "expires_at", "DATETIME"},
	} {
		if err := s.ensureColumn(column.table, column.name, column.definition); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_license_cards_owner ON license_cards(owner_account_id)`); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) UpsertRemoteDeviceState(state *RemoteDeviceState, now time.Time) error {
	if state == nil {
		return fmt.Errorf("remote state is required")
	}
	deviceID := strings.TrimSpace(state.DeviceID)
	if deviceID == "" {
		return fmt.Errorf("device id is required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	capabilities, err := json.Marshal(state.Capabilities)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO license_device_remote_state (
			device_id, supported, enabled, client_version, capabilities_json, device_public_key,
			last_heartbeat_at, last_activation_id, owner_account_id, owner_username,
			last_snapshot_status, last_snapshot_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
			supported=excluded.supported,
			enabled=excluded.enabled,
			client_version=excluded.client_version,
			capabilities_json=excluded.capabilities_json,
			device_public_key=excluded.device_public_key,
			last_heartbeat_at=excluded.last_heartbeat_at,
			last_activation_id=excluded.last_activation_id,
			owner_account_id=excluded.owner_account_id,
			owner_username=excluded.owner_username,
			last_snapshot_status=excluded.last_snapshot_status,
			last_snapshot_at=COALESCE(excluded.last_snapshot_at, license_device_remote_state.last_snapshot_at),
			updated_at=excluded.updated_at
	`, deviceID, boolInt(state.Supported), boolInt(state.Enabled), strings.TrimSpace(state.ClientVersion), string(capabilities),
		strings.TrimSpace(state.DevicePublicKey), formatTime(state.LastHeartbeatAt), state.LastActivationID,
		state.OwnerAccountID, strings.TrimSpace(state.OwnerUsername), strings.TrimSpace(state.LastSnapshotStatus),
		nullableFormattedTime(state.LastSnapshotAt), formatTime(now))
	return err
}

func (s *SQLiteStore) UpsertRemoteSnapshot(deviceID string, snapshot RemoteSnapshot, now time.Time) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return fmt.Errorf("device id is required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	snapshot.UpdatedAt = now.UTC()
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.Exec(`
		INSERT INTO license_remote_snapshots (device_id, snapshot_json, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET snapshot_json=excluded.snapshot_json, updated_at=excluded.updated_at
	`, deviceID, string(data), formatTime(now)); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE license_device_remote_state
		SET last_snapshot_status='ok', last_snapshot_at=?, updated_at=?
		WHERE device_id=?
	`, formatTime(now), formatTime(now), deviceID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLiteStore) GetRemoteDevice(deviceID string) (*RemoteDeviceState, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil, sql.ErrNoRows
	}
	row := s.db.QueryRow(`
		SELECT device_id, supported, enabled, COALESCE(client_version,''), COALESCE(capabilities_json,'[]'),
			COALESCE(device_public_key,''), last_heartbeat_at, last_activation_id, owner_account_id,
			COALESCE(owner_username,''), COALESCE(last_snapshot_status,''), last_snapshot_at,
			COALESCE(last_command_status,''), last_command_updated_at
		FROM license_device_remote_state
		WHERE device_id=?
	`, deviceID)
	state, err := scanRemoteDeviceState(row)
	if err != nil {
		return nil, err
	}
	snapshot, _ := s.GetRemoteSnapshot(deviceID)
	if snapshot != nil {
		state.Snapshot = *snapshot
	}
	return state, nil
}

func (s *SQLiteStore) GetRemoteSnapshot(deviceID string) (*RemoteSnapshot, error) {
	row := s.db.QueryRow(`SELECT snapshot_json, updated_at FROM license_remote_snapshots WHERE device_id=?`, strings.TrimSpace(deviceID))
	var raw string
	var updatedAt sql.NullTime
	if err := row.Scan(&raw, &updatedAt); err != nil {
		return nil, err
	}
	var snapshot RemoteSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return nil, err
	}
	if updatedAt.Valid {
		snapshot.UpdatedAt = updatedAt.Time.UTC()
	}
	return &snapshot, nil
}

func (s *SQLiteStore) RecordEndpointErrorTelemetry(deviceID string, activationID, ownerAccountID int64, platform, appVersion string, items []EndpointErrorTelemetryItem, now time.Time) (int, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" || len(items) == 0 {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	accepted := 0
	for _, item := range items {
		item = normalizeEndpointErrorTelemetryItem(item)
		if item.EndpointFingerprint == "" || item.Reason == "" || item.Count <= 0 || item.WindowStart.IsZero() || item.WindowEnd.IsZero() {
			continue
		}
		if item.FirstAt.IsZero() {
			item.FirstAt = item.WindowStart
		}
		if item.LastAt.IsZero() {
			item.LastAt = item.FirstAt
		}
		if _, err := tx.Exec(`
			INSERT INTO license_endpoint_error_windows (
				device_id, activation_id, owner_account_id, platform, app_version, endpoint_name,
				endpoint_fingerprint, api_host, api_url_fingerprint, auth_mode, transformer,
				model, reason, status_code, count, first_at, last_at, window_start, window_end,
				sample, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(device_id, endpoint_fingerprint, reason, status_code, window_start) DO UPDATE SET
				activation_id=excluded.activation_id,
				owner_account_id=excluded.owner_account_id,
				platform=excluded.platform,
				app_version=excluded.app_version,
				endpoint_name=excluded.endpoint_name,
				api_host=excluded.api_host,
				api_url_fingerprint=excluded.api_url_fingerprint,
				auth_mode=excluded.auth_mode,
				transformer=excluded.transformer,
				model=excluded.model,
				count=MAX(license_endpoint_error_windows.count, excluded.count),
				first_at=MIN(license_endpoint_error_windows.first_at, excluded.first_at),
				last_at=MAX(license_endpoint_error_windows.last_at, excluded.last_at),
				window_end=excluded.window_end,
				sample=CASE WHEN excluded.sample != '' THEN excluded.sample ELSE license_endpoint_error_windows.sample END,
				updated_at=excluded.updated_at
		`, deviceID, activationID, ownerAccountID, strings.TrimSpace(platform), strings.TrimSpace(appVersion), item.EndpointName,
			item.EndpointFingerprint, item.APIHost, item.APIURLFingerprint, item.AuthMode, item.Transformer, item.Model,
			item.Reason, item.StatusCode, item.Count, formatTime(item.FirstAt), formatTime(item.LastAt), formatTime(item.WindowStart),
			formatTime(item.WindowEnd), item.Sample, formatTime(now), formatTime(now)); err != nil {
			return 0, err
		}
		accepted++
	}
	_, _ = tx.Exec(`DELETE FROM license_endpoint_error_windows WHERE window_end < ?`, formatTime(now.AddDate(0, 0, -90)))
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return accepted, nil
}

func (s *SQLiteStore) ListEndpointErrorTelemetry(query EndpointErrorTelemetryQuery) ([]EndpointErrorTelemetryRecord, error) {
	sqlText, args := endpointErrorTelemetrySelectSQL(query)
	rows, err := s.db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]EndpointErrorTelemetryRecord, 0)
	for rows.Next() {
		record, err := scanEndpointErrorTelemetryRecord(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *record)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) SummarizeEndpointErrorTelemetry(query EndpointErrorTelemetryQuery) ([]EndpointErrorTelemetrySummary, error) {
	sqlText, args := endpointErrorTelemetrySummarySQL(query)
	rows, err := s.db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]EndpointErrorTelemetrySummary, 0)
	for rows.Next() {
		var summary EndpointErrorTelemetrySummary
		var lastAt string
		if err := rows.Scan(&summary.DeviceID, &summary.EndpointName, &summary.EndpointFingerprint, &summary.APIHost,
			&summary.Reason, &summary.StatusCode, &summary.Count, &lastAt, &summary.Sample); err != nil {
			return nil, err
		}
		summary.LastAt = parseTime(lastAt)
		result = append(result, summary)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) CreateRemoteCommand(command *RemoteCommandRecord, ownerAccountID int64) error {
	if command == nil {
		return fmt.Errorf("remote command is required")
	}
	now := command.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	command.CreatedAt = now.UTC()
	command.UpdatedAt = now.UTC()
	if command.Status == "" {
		command.Status = RemoteCommandStatusQueued
	}
	envelope, err := json.Marshal(command.Envelope)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`
		INSERT INTO license_remote_commands (
			device_id, command_type, status, actor_account_id, actor_username, owner_account_id,
			envelope_json, summary_json, result, result_json, error, expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, strings.TrimSpace(command.DeviceID), strings.TrimSpace(command.CommandType), command.Status, command.ActorID,
		strings.TrimSpace(command.ActorName), ownerAccountID, string(envelope), remoteCommandSummaryJSON(command.Summary),
		command.Result, remoteCommandResultJSON(command.ResultJSON), command.Error,
		nullableFormattedTime(command.ExpiresAt),
		formatTime(command.CreatedAt), formatTime(command.UpdatedAt))
	if err != nil {
		return err
	}
	command.ID, err = result.LastInsertId()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		UPDATE license_device_remote_state
		SET last_command_status=?, last_command_updated_at=?, updated_at=?
		WHERE device_id=?
	`, command.Status, formatTime(command.UpdatedAt), formatTime(command.UpdatedAt), strings.TrimSpace(command.DeviceID))
	return err
}

func (s *SQLiteStore) ListRemoteCommands(deviceID string, limit int) ([]RemoteCommandRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, device_id, command_type, status, actor_account_id, COALESCE(actor_username,''),
			envelope_json, COALESCE(summary_json,''), COALESCE(result,''), COALESCE(result_json,''), COALESCE(error,''), expires_at, created_at, updated_at
		FROM license_remote_commands
		WHERE device_id=?
		ORDER BY id DESC
		LIMIT ?
	`, strings.TrimSpace(deviceID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RemoteCommandRecord, 0)
	for rows.Next() {
		command, err := scanRemoteCommand(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *command)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) ListQueuedRemoteCommands(deviceID string, limit int) ([]RemoteCommandRecord, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`
		SELECT id, device_id, command_type, status, actor_account_id, COALESCE(actor_username,''),
			envelope_json, COALESCE(summary_json,''), COALESCE(result,''), COALESCE(result_json,''), COALESCE(error,''), expires_at, created_at, updated_at
		FROM license_remote_commands
		WHERE device_id=? AND status=?
		ORDER BY id ASC
		LIMIT ?
	`, strings.TrimSpace(deviceID), RemoteCommandStatusQueued, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RemoteCommandRecord, 0)
	for rows.Next() {
		command, err := scanRemoteCommand(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *command)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) MarkRemoteCommandsDelivered(deviceID string, commandIDs []int64, now time.Time) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" || len(commandIDs) == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	for _, id := range commandIDs {
		if id <= 0 {
			continue
		}
		if _, err := tx.Exec(`
			UPDATE license_remote_commands
			SET status=?, updated_at=?
			WHERE id=? AND device_id=? AND status=?
		`, RemoteCommandStatusDelivered, formatTime(now), id, deviceID, RemoteCommandStatusQueued); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		UPDATE license_device_remote_state
		SET last_command_status=?, last_command_updated_at=?, updated_at=?
		WHERE device_id=?
	`, RemoteCommandStatusDelivered, formatTime(now), formatTime(now), deviceID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLiteStore) GetRemoteCommand(deviceID string, commandID int64) (*RemoteCommandRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, device_id, command_type, status, actor_account_id, COALESCE(actor_username,''),
			envelope_json, COALESCE(summary_json,''), COALESCE(result,''), COALESCE(result_json,''), COALESCE(error,''), expires_at, created_at, updated_at
		FROM license_remote_commands
		WHERE device_id=? AND id=?
	`, strings.TrimSpace(deviceID), commandID)
	return scanRemoteCommand(row)
}

func (s *SQLiteStore) UpdateRemoteCommandResult(deviceID string, commandID int64, status, resultText, errorText string, resultJSON *RemoteCommandResult, expiresAt time.Time, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	res, err := s.db.Exec(`
		UPDATE license_remote_commands
		SET status=?, result=?, result_json=?, error=?, expires_at=?, updated_at=?
		WHERE id=? AND device_id=?
	`, strings.TrimSpace(status), strings.TrimSpace(resultText), remoteCommandResultJSON(resultJSON),
		strings.TrimSpace(errorText), nullableFormattedTime(expiresAt), formatTime(now), commandID, strings.TrimSpace(deviceID))
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	_, err = s.db.Exec(`
		UPDATE license_device_remote_state
		SET last_command_status=?, last_command_updated_at=?, updated_at=?
		WHERE device_id=?
	`, strings.TrimSpace(status), formatTime(now), formatTime(now), strings.TrimSpace(deviceID))
	return err
}

func scanRemoteDeviceState(scanner interface {
	Scan(dest ...interface{}) error
}) (*RemoteDeviceState, error) {
	var state RemoteDeviceState
	var supported, enabled int
	var capabilitiesJSON string
	var lastHeartbeat, lastSnapshot, lastCommand sql.NullTime
	if err := scanner.Scan(&state.DeviceID, &supported, &enabled, &state.ClientVersion, &capabilitiesJSON,
		&state.DevicePublicKey, &lastHeartbeat, &state.LastActivationID, &state.OwnerAccountID,
		&state.OwnerUsername, &state.LastSnapshotStatus, &lastSnapshot, &state.LastCommandStatus, &lastCommand); err != nil {
		return nil, err
	}
	state.Supported = supported != 0
	state.Enabled = enabled != 0
	_ = json.Unmarshal([]byte(capabilitiesJSON), &state.Capabilities)
	if lastHeartbeat.Valid {
		state.LastHeartbeatAt = lastHeartbeat.Time.UTC()
	}
	if lastSnapshot.Valid {
		state.LastSnapshotAt = lastSnapshot.Time.UTC()
	}
	if lastCommand.Valid {
		state.LastCommandUpdatedAt = lastCommand.Time.UTC()
	}
	return &state, nil
}

func endpointErrorTelemetrySelectSQL(query EndpointErrorTelemetryQuery) (string, []interface{}) {
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	sqlText := `
		SELECT id, device_id, activation_id, owner_account_id, COALESCE(platform,''), COALESCE(app_version,''),
			endpoint_name, endpoint_fingerprint, COALESCE(api_host,''), COALESCE(api_url_fingerprint,''),
			COALESCE(auth_mode,''), COALESCE(transformer,''), COALESCE(model,''), reason, status_code,
			count, first_at, last_at, window_start, window_end, COALESCE(sample,''), created_at, updated_at
		FROM license_endpoint_error_windows
	`
	where, args := endpointErrorTelemetryWhere(query)
	sqlText += where + ` ORDER BY window_start DESC, id DESC LIMIT ?`
	args = append(args, limit)
	return sqlText, args
}

func endpointErrorTelemetrySummarySQL(query EndpointErrorTelemetryQuery) (string, []interface{}) {
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	where, args := endpointErrorTelemetryWhere(query)
	sqlText := `
		SELECT device_id, endpoint_name, endpoint_fingerprint, COALESCE(api_host,''), reason, status_code,
			SUM(count), MAX(last_at), COALESCE(MAX(sample), '')
		FROM license_endpoint_error_windows
	` + where + `
		GROUP BY device_id, endpoint_fingerprint, reason, status_code
		ORDER BY MAX(last_at) DESC
		LIMIT ?
	`
	args = append(args, limit)
	return sqlText, args
}

func endpointErrorTelemetryWhere(query EndpointErrorTelemetryQuery) (string, []interface{}) {
	clauses := make([]string, 0)
	args := make([]interface{}, 0)
	if strings.TrimSpace(query.DeviceID) != "" {
		clauses = append(clauses, "device_id=?")
		args = append(args, strings.TrimSpace(query.DeviceID))
	}
	if !query.From.IsZero() {
		clauses = append(clauses, "window_end>=?")
		args = append(args, formatTime(query.From))
	}
	if !query.To.IsZero() {
		clauses = append(clauses, "window_start<=?")
		args = append(args, formatTime(query.To))
	}
	if strings.TrimSpace(query.EndpointName) != "" {
		clauses = append(clauses, "endpoint_name=?")
		args = append(args, strings.TrimSpace(query.EndpointName))
	}
	if strings.TrimSpace(query.Reason) != "" {
		clauses = append(clauses, "reason=?")
		args = append(args, strings.TrimSpace(query.Reason))
	}
	if query.StatusCodeSet {
		clauses = append(clauses, "status_code=?")
		args = append(args, query.StatusCode)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func scanEndpointErrorTelemetryRecord(scanner interface {
	Scan(dest ...interface{}) error
}) (*EndpointErrorTelemetryRecord, error) {
	var record EndpointErrorTelemetryRecord
	var firstAt, lastAt, windowStart, windowEnd, createdAt, updatedAt sql.NullTime
	if err := scanner.Scan(
		&record.ID,
		&record.DeviceID,
		&record.ActivationID,
		&record.OwnerAccountID,
		&record.Platform,
		&record.AppVersion,
		&record.EndpointName,
		&record.EndpointFingerprint,
		&record.APIHost,
		&record.APIURLFingerprint,
		&record.AuthMode,
		&record.Transformer,
		&record.Model,
		&record.Reason,
		&record.StatusCode,
		&record.Count,
		&firstAt,
		&lastAt,
		&windowStart,
		&windowEnd,
		&record.Sample,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	if firstAt.Valid {
		record.FirstAt = firstAt.Time.UTC()
	}
	if lastAt.Valid {
		record.LastAt = lastAt.Time.UTC()
	}
	if windowStart.Valid {
		record.WindowStart = windowStart.Time.UTC()
	}
	if windowEnd.Valid {
		record.WindowEnd = windowEnd.Time.UTC()
	}
	if createdAt.Valid {
		record.CreatedAt = createdAt.Time.UTC()
	}
	if updatedAt.Valid {
		record.UpdatedAt = updatedAt.Time.UTC()
	}
	return &record, nil
}

func scanRemoteCommand(scanner interface {
	Scan(dest ...interface{}) error
}) (*RemoteCommandRecord, error) {
	var command RemoteCommandRecord
	var rawEnvelope string
	var rawSummary string
	var rawResult string
	var expiresAt sql.NullTime
	if err := scanner.Scan(&command.ID, &command.DeviceID, &command.CommandType, &command.Status, &command.ActorID,
		&command.ActorName, &rawEnvelope, &rawSummary, &command.Result, &rawResult, &command.Error, &expiresAt, &command.CreatedAt, &command.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(rawEnvelope), &command.Envelope)
	if strings.TrimSpace(rawSummary) != "" {
		var summary RemoteCommandSummary
		if err := json.Unmarshal([]byte(rawSummary), &summary); err == nil {
			command.Summary = &summary
		}
	}
	if strings.TrimSpace(rawResult) != "" {
		var result RemoteCommandResult
		if err := json.Unmarshal([]byte(rawResult), &result); err == nil {
			command.ResultJSON = &result
		}
	}
	if expiresAt.Valid {
		command.ExpiresAt = expiresAt.Time.UTC()
	}
	command.CreatedAt = command.CreatedAt.UTC()
	command.UpdatedAt = command.UpdatedAt.UTC()
	return &command, nil
}

func normalizeEndpointErrorTelemetryItem(item EndpointErrorTelemetryItem) EndpointErrorTelemetryItem {
	item.EndpointName = strings.TrimSpace(item.EndpointName)
	item.EndpointFingerprint = strings.TrimSpace(item.EndpointFingerprint)
	item.APIHost = strings.TrimSpace(item.APIHost)
	item.APIURLFingerprint = strings.TrimSpace(item.APIURLFingerprint)
	item.AuthMode = strings.TrimSpace(item.AuthMode)
	item.Transformer = strings.TrimSpace(item.Transformer)
	item.Model = strings.TrimSpace(item.Model)
	item.Reason = strings.TrimSpace(item.Reason)
	item.Sample = strings.TrimSpace(item.Sample)
	return item
}

func remoteCommandResultJSON(result *RemoteCommandResult) interface{} {
	if result == nil {
		return nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil
	}
	return string(data)
}

func remoteCommandSummaryJSON(summary *RemoteCommandSummary) interface{} {
	if summary == nil {
		return nil
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return nil
	}
	return string(data)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableFormattedTime(value time.Time) interface{} {
	if value.IsZero() {
		return nil
	}
	return formatTime(value)
}

func (s *SQLiteStore) ensureColumn(table, name, definition string) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var columnName string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if columnName == name {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + name + ` ` + definition)
	return err
}

func (s *SQLiteStore) CreateCard(card *CardRecord) error {
	if card == nil {
		return fmt.Errorf("card is required")
	}
	result, err := s.db.Exec(`
		INSERT INTO license_cards (card_hash, plan, days, max_devices, status, customer, remark, owner_account_id, created_by_account_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, card.CardHash, string(card.Plan), card.Days, card.MaxDevices, card.Status, card.Customer, card.Remark, card.OwnerAccountID, card.CreatedByAccountID, formatTime(card.CreatedAt))
	if err != nil {
		return err
	}
	card.ID, err = result.LastInsertId()
	return err
}

func (s *SQLiteStore) FindCardByHash(hash string) (*CardRecord, error) {
	row := s.db.QueryRow(`
		SELECT c.id, c.card_hash, c.plan, c.days, c.max_devices, c.status, COALESCE(c.customer, ''), COALESCE(c.remark, ''),
			c.owner_account_id, c.created_by_account_id, COALESCE(a.username, ''), c.created_at, c.disabled_at
		FROM license_cards c
		LEFT JOIN admin_accounts a ON a.id = c.owner_account_id
		WHERE c.card_hash = ?
	`, hash)
	return scanCard(row)
}

func (s *SQLiteStore) GetCard(id int64) (*CardRecord, error) {
	row := s.db.QueryRow(`
		SELECT c.id, c.card_hash, c.plan, c.days, c.max_devices, c.status, COALESCE(c.customer, ''), COALESCE(c.remark, ''),
			c.owner_account_id, c.created_by_account_id, COALESCE(owner.username, ''), c.created_at, c.disabled_at,
			COUNT(a.id)
		FROM license_cards c
		LEFT JOIN license_activations a ON a.card_id = c.id AND a.status = ?
		LEFT JOIN admin_accounts owner ON owner.id = c.owner_account_id
		WHERE c.id = ?
		GROUP BY c.id
	`, ActivationStatusActive, id)
	return scanCardWithCount(row)
}

func (s *SQLiteStore) ListCards() ([]CardRecord, error) {
	rows, err := s.db.Query(`
		SELECT c.id, c.card_hash, c.plan, c.days, c.max_devices, c.status, COALESCE(c.customer, ''), COALESCE(c.remark, ''),
			c.owner_account_id, c.created_by_account_id, COALESCE(owner.username, ''), c.created_at, c.disabled_at,
			COUNT(a.id)
		FROM license_cards c
		LEFT JOIN license_activations a ON a.card_id = c.id AND a.status = ?
		LEFT JOIN admin_accounts owner ON owner.id = c.owner_account_id
		GROUP BY c.id
		ORDER BY c.id DESC
	`, ActivationStatusActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]CardRecord, 0)
	for rows.Next() {
		card, err := scanCardWithCount(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *card)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) ListCardsByOwner(ownerIDs []int64) ([]CardRecord, error) {
	if len(ownerIDs) == 0 {
		return []CardRecord{}, nil
	}
	placeholders, args := int64Placeholders(ownerIDs)
	query := `
		SELECT c.id, c.card_hash, c.plan, c.days, c.max_devices, c.status, COALESCE(c.customer, ''), COALESCE(c.remark, ''),
			c.owner_account_id, c.created_by_account_id, COALESCE(owner.username, ''), c.created_at, c.disabled_at,
			COUNT(a.id)
		FROM license_cards c
		LEFT JOIN license_activations a ON a.card_id = c.id AND a.status = ?
		LEFT JOIN admin_accounts owner ON owner.id = c.owner_account_id
		WHERE c.owner_account_id IN (` + placeholders + `)
		GROUP BY c.id
		ORDER BY c.id DESC
	`
	queryArgs := append([]interface{}{ActivationStatusActive}, args...)
	rows, err := s.db.Query(query, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]CardRecord, 0)
	for rows.Next() {
		card, err := scanCardWithCount(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *card)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) DisableCard(id int64, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	result, err := tx.Exec(`UPDATE license_cards SET status = ?, disabled_at = ? WHERE id = ?`, CardStatusDisabled, formatTime(now), id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrInvalidCard
	}
	if _, err := tx.Exec(`
		UPDATE license_activations
		SET status = ?, expires_at = CASE WHEN expires_at > ? THEN ? ELSE expires_at END,
			disabled_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE card_id = ? AND status = ?
	`, ActivationStatusDisabled, formatTime(now), formatTime(now), formatTime(now), id, ActivationStatusActive); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLiteStore) DeleteCard(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.Exec(`DELETE FROM license_activations WHERE card_id = ?`, id); err != nil {
		return err
	}
	result, err := tx.Exec(`DELETE FROM license_cards WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrInvalidCard
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLiteStore) ActivateCard(cardHash, deviceID string, now time.Time, platform, appVersion, ipAddress string) (*ActivationRecord, error) {
	if deviceID == "" {
		return nil, fmt.Errorf("device id is required")
	}
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout=5000`); err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return nil, err
	}
	tx := &sqliteImmediateTx{conn: conn, ctx: ctx}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	card, err := findCardByHashTx(tx, cardHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidCard
		}
		return nil, err
	}
	if card.Status != CardStatusActive {
		return nil, ErrCardDisabled
	}

	existing, err := findActivationTx(tx, card.ID, deviceID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil && existing.Status == ActivationStatusDisabled {
		return nil, ErrActivationBlocked
	}
	if err == nil {
		existing.LastCheckedAt = now
		existing.Platform = platform
		existing.AppVersion = appVersion
		existing.IPAddress = ipAddress
		if err := upsertActivationTx(tx, existing); err != nil {
			return nil, err
		}
		if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
			return nil, err
		}
		committed = true
		return existing, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		count, err := activeActivationCountTx(tx, card.ID)
		if err != nil {
			return nil, err
		}
		if count >= card.MaxDevices {
			return nil, ErrDeviceLimit
		}
		existing = &ActivationRecord{
			CardID:      card.ID,
			DeviceID:    deviceID,
			Status:      ActivationStatusActive,
			ActivatedAt: now,
		}
	}

	base := now
	if latest, err := latestActivationForDeviceTx(tx, deviceID); err == nil && latest.ExpiresAt.After(now) {
		base = latest.ExpiresAt
	}
	existing.Status = ActivationStatusActive
	existing.ExpiresAt = base.AddDate(0, 0, card.Days)
	existing.LastCheckedAt = now
	existing.Platform = platform
	existing.AppVersion = appVersion
	existing.IPAddress = ipAddress
	if err := upsertActivationTx(tx, existing); err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return nil, err
	}
	committed = true
	return existing, nil
}

func (s *SQLiteStore) ActiveActivationCount(cardID int64) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM license_activations WHERE card_id = ? AND status = ?`, cardID, ActivationStatusActive).Scan(&count)
	return count, err
}

func (s *SQLiteStore) FindActivation(cardID int64, deviceID string) (*ActivationRecord, error) {
	row := s.db.QueryRow(`
		SELECT a.id, a.card_id, a.device_id, a.status, a.activated_at, a.expires_at, a.last_checked_at, a.disabled_at,
			COALESCE(a.platform, ''), COALESCE(a.app_version, ''), COALESCE(a.ip_address, ''),
			c.status, c.plan, c.days,
			COALESCE(c.customer, ''), COALESCE(c.remark, ''), c.owner_account_id, COALESCE(owner.username, '')
		FROM license_activations a
		INNER JOIN license_cards c ON c.id = a.card_id
		LEFT JOIN admin_accounts owner ON owner.id = c.owner_account_id
		WHERE a.card_id = ? AND a.device_id = ?
	`, cardID, deviceID)
	return scanActivation(row)
}

func (s *SQLiteStore) GetActivation(id int64) (*ActivationRecord, error) {
	row := s.db.QueryRow(`
		SELECT a.id, a.card_id, a.device_id, a.status, a.activated_at, a.expires_at, a.last_checked_at, a.disabled_at,
			COALESCE(a.platform, ''), COALESCE(a.app_version, ''), COALESCE(a.ip_address, ''),
			c.status, c.plan, c.days,
			COALESCE(c.customer, ''), COALESCE(c.remark, ''), c.owner_account_id, COALESCE(owner.username, '')
		FROM license_activations a
		INNER JOIN license_cards c ON c.id = a.card_id
		LEFT JOIN admin_accounts owner ON owner.id = c.owner_account_id
		WHERE a.id = ?
	`, id)
	return scanActivation(row)
}

func (s *SQLiteStore) LatestActivationForDevice(deviceID string) (*ActivationRecord, error) {
	row := s.db.QueryRow(`
		SELECT a.id, a.card_id, a.device_id, a.status, a.activated_at, a.expires_at, a.last_checked_at, a.disabled_at,
			COALESCE(a.platform, ''), COALESCE(a.app_version, ''), COALESCE(a.ip_address, ''),
			c.status, c.plan, c.days,
			COALESCE(c.customer, ''), COALESCE(c.remark, ''), c.owner_account_id, COALESCE(owner.username, '')
		FROM license_activations a
		INNER JOIN license_cards c ON c.id = a.card_id
		LEFT JOIN admin_accounts owner ON owner.id = c.owner_account_id
		WHERE a.device_id = ? AND a.status = ?
		ORDER BY a.expires_at DESC
		LIMIT 1
	`, deviceID, ActivationStatusActive)
	return scanActivation(row)
}

func (s *SQLiteStore) ListActivations() ([]ActivationRecord, error) {
	rows, err := s.db.Query(`
		SELECT a.id, a.card_id, a.device_id, a.status, a.activated_at, a.expires_at, a.last_checked_at, a.disabled_at,
			COALESCE(a.platform, ''), COALESCE(a.app_version, ''), COALESCE(a.ip_address, ''),
			c.status, c.plan, c.days,
			COALESCE(c.customer, ''), COALESCE(c.remark, ''), c.owner_account_id, COALESCE(owner.username, '')
		FROM license_activations a
		INNER JOIN license_cards c ON c.id = a.card_id
		LEFT JOIN admin_accounts owner ON owner.id = c.owner_account_id
		ORDER BY a.id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]ActivationRecord, 0)
	for rows.Next() {
		activation, err := scanActivation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *activation)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) ListActivationsByOwner(ownerIDs []int64) ([]ActivationRecord, error) {
	if len(ownerIDs) == 0 {
		return []ActivationRecord{}, nil
	}
	placeholders, args := int64Placeholders(ownerIDs)
	query := `
		SELECT a.id, a.card_id, a.device_id, a.status, a.activated_at, a.expires_at, a.last_checked_at, a.disabled_at,
			COALESCE(a.platform, ''), COALESCE(a.app_version, ''), COALESCE(a.ip_address, ''),
			c.status, c.plan, c.days,
			COALESCE(c.customer, ''), COALESCE(c.remark, ''), c.owner_account_id, COALESCE(owner.username, '')
		FROM license_activations a
		INNER JOIN license_cards c ON c.id = a.card_id
		LEFT JOIN admin_accounts owner ON owner.id = c.owner_account_id
		WHERE c.owner_account_id IN (` + placeholders + `)
		ORDER BY a.id DESC
	`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]ActivationRecord, 0)
	for rows.Next() {
		activation, err := scanActivation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *activation)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) UpsertActivation(activation *ActivationRecord) error {
	if activation == nil {
		return fmt.Errorf("activation is required")
	}
	return upsertActivation(s.db, activation)
}

type dbRunner interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

type sqliteImmediateTx struct {
	conn *sql.Conn
	ctx  context.Context
}

func (tx *sqliteImmediateTx) Exec(query string, args ...interface{}) (sql.Result, error) {
	return tx.conn.ExecContext(tx.ctx, query, args...)
}

func (tx *sqliteImmediateTx) QueryRow(query string, args ...interface{}) *sql.Row {
	return tx.conn.QueryRowContext(tx.ctx, query, args...)
}

func findCardByHashTx(tx dbRunner, hash string) (*CardRecord, error) {
	row := tx.QueryRow(`
		SELECT c.id, c.card_hash, c.plan, c.days, c.max_devices, c.status, COALESCE(c.customer, ''), COALESCE(c.remark, ''),
			c.owner_account_id, c.created_by_account_id, COALESCE(owner.username, ''), c.created_at, c.disabled_at
		FROM license_cards c
		LEFT JOIN admin_accounts owner ON owner.id = c.owner_account_id
		WHERE c.card_hash = ?
	`, hash)
	return scanCard(row)
}

func activeActivationCountTx(tx dbRunner, cardID int64) (int, error) {
	var count int
	err := tx.QueryRow(`SELECT COUNT(*) FROM license_activations WHERE card_id = ? AND status = ?`, cardID, ActivationStatusActive).Scan(&count)
	return count, err
}

func findActivationTx(tx dbRunner, cardID int64, deviceID string) (*ActivationRecord, error) {
	row := tx.QueryRow(`
		SELECT a.id, a.card_id, a.device_id, a.status, a.activated_at, a.expires_at, a.last_checked_at, a.disabled_at,
			COALESCE(a.platform, ''), COALESCE(a.app_version, ''), COALESCE(a.ip_address, ''),
			c.status, c.plan, c.days,
			COALESCE(c.customer, ''), COALESCE(c.remark, ''), c.owner_account_id, COALESCE(owner.username, '')
		FROM license_activations a
		INNER JOIN license_cards c ON c.id = a.card_id
		LEFT JOIN admin_accounts owner ON owner.id = c.owner_account_id
		WHERE a.card_id = ? AND a.device_id = ?
	`, cardID, deviceID)
	return scanActivation(row)
}

func latestActivationForDeviceTx(tx dbRunner, deviceID string) (*ActivationRecord, error) {
	row := tx.QueryRow(`
		SELECT a.id, a.card_id, a.device_id, a.status, a.activated_at, a.expires_at, a.last_checked_at, a.disabled_at,
			COALESCE(a.platform, ''), COALESCE(a.app_version, ''), COALESCE(a.ip_address, ''),
			c.status, c.plan, c.days,
			COALESCE(c.customer, ''), COALESCE(c.remark, ''), c.owner_account_id, COALESCE(owner.username, '')
		FROM license_activations a
		INNER JOIN license_cards c ON c.id = a.card_id
		LEFT JOIN admin_accounts owner ON owner.id = c.owner_account_id
		WHERE a.device_id = ? AND a.status = ?
		ORDER BY a.expires_at DESC
		LIMIT 1
	`, deviceID, ActivationStatusActive)
	return scanActivation(row)
}

func upsertActivationTx(tx dbRunner, activation *ActivationRecord) error {
	return upsertActivation(tx, activation)
}

func upsertActivation(db dbRunner, activation *ActivationRecord) error {
	if activation == nil {
		return fmt.Errorf("activation is required")
	}
	result, err := db.Exec(`
		INSERT INTO license_activations (
			card_id, device_id, status, activated_at, expires_at, last_checked_at, platform, app_version, ip_address
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(card_id, device_id) DO UPDATE SET
			status=excluded.status,
			expires_at=excluded.expires_at,
			last_checked_at=excluded.last_checked_at,
			platform=excluded.platform,
			app_version=excluded.app_version,
			ip_address=excluded.ip_address,
			updated_at=CURRENT_TIMESTAMP
	`, activation.CardID, activation.DeviceID, activation.Status, formatTime(activation.ActivatedAt), formatTime(activation.ExpiresAt),
		formatTime(activation.LastCheckedAt), activation.Platform, activation.AppVersion, activation.IPAddress)
	if err != nil {
		return err
	}
	if activation.ID == 0 {
		if id, err := result.LastInsertId(); err == nil && id > 0 {
			activation.ID = id
		}
	}
	if activation.ID == 0 {
		existing, err := findActivationForRunner(db, activation.CardID, activation.DeviceID)
		if err != nil {
			return err
		}
		activation.ID = existing.ID
	}
	return nil
}

func findActivationForRunner(db dbRunner, cardID int64, deviceID string) (*ActivationRecord, error) {
	row := db.QueryRow(`
		SELECT a.id, a.card_id, a.device_id, a.status, a.activated_at, a.expires_at, a.last_checked_at, a.disabled_at,
			COALESCE(a.platform, ''), COALESCE(a.app_version, ''), COALESCE(a.ip_address, ''),
			c.status, c.plan, c.days,
			COALESCE(c.customer, ''), COALESCE(c.remark, ''), c.owner_account_id, COALESCE(owner.username, '')
		FROM license_activations a
		INNER JOIN license_cards c ON c.id = a.card_id
		LEFT JOIN admin_accounts owner ON owner.id = c.owner_account_id
		WHERE a.card_id = ? AND a.device_id = ?
	`, cardID, deviceID)
	return scanActivation(row)
}

func (s *SQLiteStore) TouchActivation(id int64, now time.Time, platform, appVersion, ipAddress string) error {
	_, err := s.db.Exec(`
		UPDATE license_activations
		SET last_checked_at = ?, platform = ?, app_version = ?, ip_address = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, formatTime(now), platform, appVersion, ipAddress, id)
	return err
}

func (s *SQLiteStore) DisableActivation(id int64, now time.Time) error {
	result, err := s.db.Exec(`
		UPDATE license_activations
		SET status = ?, expires_at = CASE WHEN expires_at > ? THEN ? ELSE expires_at END,
			disabled_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, ActivationStatusDisabled, formatTime(now), formatTime(now), formatTime(now), id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) SetDeviceExpiry(deviceID string, expiresAt, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var currentID int64
	var currentExpiry string
	err = tx.QueryRow(`
		SELECT id, expires_at
		FROM license_activations
		WHERE device_id = ? AND status = ?
		ORDER BY expires_at DESC, id DESC
		LIMIT 1
	`, deviceID, ActivationStatusActive).Scan(&currentID, &currentExpiry)
	if err != nil {
		return err
	}

	if !expiresAt.After(now) {
		if _, err := tx.Exec(`
			UPDATE license_activations
			SET status = ?, expires_at = ?, disabled_at = ?, updated_at = CURRENT_TIMESTAMP
			WHERE device_id = ? AND status = ?
		`, ActivationStatusDisabled, formatTime(expiresAt), formatTime(now), deviceID, ActivationStatusActive); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`
			UPDATE license_activations
			SET expires_at = ?, updated_at = CURRENT_TIMESTAMP
			WHERE device_id = ? AND status = ? AND expires_at > ?
		`, formatTime(expiresAt), deviceID, ActivationStatusActive, formatTime(expiresAt)); err != nil {
			return err
		}
		if parseTime(currentExpiry).Before(expiresAt) {
			if _, err := tx.Exec(`
				UPDATE license_activations
				SET expires_at = ?, updated_at = CURRENT_TIMESTAMP
				WHERE id = ?
			`, formatTime(expiresAt), currentID); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLiteStore) ListDeviceRemarks() (map[string]string, error) {
	rows, err := s.db.Query(`
		SELECT device_id, COALESCE(remark, '')
		FROM license_device_notes
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	remarks := make(map[string]string)
	for rows.Next() {
		var deviceID string
		var remark string
		if err := rows.Scan(&deviceID, &remark); err != nil {
			return nil, err
		}
		remarks[deviceID] = remark
	}
	return remarks, rows.Err()
}

func (s *SQLiteStore) SetDeviceRemark(deviceID, remark string, now time.Time) error {
	if remark == "" {
		_, err := s.db.Exec(`DELETE FROM license_device_notes WHERE device_id = ?`, deviceID)
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO license_device_notes (device_id, remark, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
			remark=excluded.remark,
			updated_at=excluded.updated_at
	`, deviceID, remark, formatTime(now))
	return err
}

func (s *SQLiteStore) AddAudit(action, targetType string, targetID int64, detail string, now time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO admin_audit_logs (action, target_type, target_id, detail, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, action, targetType, targetID, detail, formatTime(now))
	return err
}

func (s *SQLiteStore) ListAudit(limit int) ([]AuditRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.Query(`
		SELECT id, action, target_type, target_id, COALESCE(detail, ''), created_at
		FROM admin_audit_logs
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]AuditRecord, 0)
	for rows.Next() {
		audit, err := scanAudit(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *audit)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) UpsertAdminAccount(account *AdminAccount, passwordHash string) error {
	if account == nil {
		return fmt.Errorf("admin account is required")
	}
	if account.CreatedAt.IsZero() {
		account.CreatedAt = time.Now().UTC()
	}
	if account.UpdatedAt.IsZero() {
		account.UpdatedAt = account.CreatedAt
	}
	result, err := s.db.Exec(`
		INSERT INTO admin_accounts (username, password_hash, display_name, level, parent_id, status, permissions, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET
			password_hash=excluded.password_hash,
			display_name=excluded.display_name,
			level=excluded.level,
			parent_id=excluded.parent_id,
			status=excluded.status,
			permissions=excluded.permissions,
			updated_at=excluded.updated_at
	`, account.Username, passwordHash, account.DisplayName, account.Level, account.ParentID, account.Status, encodePermissions(account.Permissions), account.CreatedBy, formatTime(account.CreatedAt), formatTime(account.UpdatedAt))
	if err != nil {
		return err
	}
	if account.ID == 0 {
		if id, err := result.LastInsertId(); err == nil && id > 0 {
			account.ID = id
		}
	}
	if account.ID == 0 {
		existing, _, err := s.GetAdminAccountByUsername(account.Username)
		if err != nil {
			return err
		}
		account.ID = existing.ID
		account.CreatedAt = existing.CreatedAt
	}
	return nil
}

func (s *SQLiteStore) GetAdminAccount(id int64) (*AdminAccount, string, error) {
	row := s.db.QueryRow(`
		SELECT id, username, password_hash, COALESCE(display_name, ''), level, parent_id, status, COALESCE(permissions, ''), created_by, created_at, updated_at
		FROM admin_accounts
		WHERE id = ?
	`, id)
	return scanAdminAccount(row)
}

func (s *SQLiteStore) GetAdminAccountByUsername(username string) (*AdminAccount, string, error) {
	row := s.db.QueryRow(`
		SELECT id, username, password_hash, COALESCE(display_name, ''), level, parent_id, status, COALESCE(permissions, ''), created_by, created_at, updated_at
		FROM admin_accounts
		WHERE username = ?
	`, username)
	return scanAdminAccount(row)
}

func (s *SQLiteStore) ListAdminAccounts() ([]AdminAccount, error) {
	rows, err := s.db.Query(`
		SELECT id, username, password_hash, COALESCE(display_name, ''), level, parent_id, status, COALESCE(permissions, ''), created_by, created_at, updated_at
		FROM admin_accounts
		ORDER BY level ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AdminAccount, 0)
	for rows.Next() {
		account, _, err := scanAdminAccount(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *account)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) UpdateAdminAccount(account *AdminAccount, passwordHash string) error {
	if account == nil {
		return fmt.Errorf("admin account is required")
	}
	if account.UpdatedAt.IsZero() {
		account.UpdatedAt = time.Now().UTC()
	}
	var result sql.Result
	var err error
	if strings.TrimSpace(passwordHash) == "" {
		result, err = s.db.Exec(`
			UPDATE admin_accounts
			SET display_name = ?, level = ?, parent_id = ?, status = ?, permissions = ?, updated_at = ?
			WHERE id = ?
		`, account.DisplayName, account.Level, account.ParentID, account.Status, encodePermissions(account.Permissions), formatTime(account.UpdatedAt), account.ID)
	} else {
		result, err = s.db.Exec(`
			UPDATE admin_accounts
			SET password_hash = ?, display_name = ?, level = ?, parent_id = ?, status = ?, permissions = ?, updated_at = ?
			WHERE id = ?
		`, passwordHash, account.DisplayName, account.Level, account.ParentID, account.Status, encodePermissions(account.Permissions), formatTime(account.UpdatedAt), account.ID)
	}
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) ClaimUnownedCards(ownerID int64) error {
	_, err := s.db.Exec(`
		UPDATE license_cards
		SET owner_account_id = CASE WHEN owner_account_id = 0 THEN ? ELSE owner_account_id END,
			created_by_account_id = CASE WHEN created_by_account_id = 0 THEN ? ELSE created_by_account_id END
		WHERE owner_account_id = 0 OR created_by_account_id = 0
	`, ownerID, ownerID)
	return err
}

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func int64Placeholders(values []int64) (string, []interface{}) {
	placeholders := make([]string, 0, len(values))
	args := make([]interface{}, 0, len(values))
	for _, value := range values {
		placeholders = append(placeholders, "?")
		args = append(args, value)
	}
	return strings.Join(placeholders, ","), args
}

func encodePermissions(permissions []string) string {
	clean := make([]string, 0, len(permissions))
	seen := map[string]bool{}
	for _, permission := range permissions {
		permission = strings.TrimSpace(permission)
		if permission == "" || seen[permission] {
			continue
		}
		seen[permission] = true
		clean = append(clean, permission)
	}
	return strings.Join(clean, ",")
}

func decodePermissions(encoded string) []string {
	if strings.TrimSpace(encoded) == "" {
		return []string{}
	}
	parts := strings.Split(encoded, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func scanAdminAccount(row rowScanner) (*AdminAccount, string, error) {
	account := &AdminAccount{}
	var passwordHash string
	var permissions string
	var createdAt string
	var updatedAt string
	if err := row.Scan(
		&account.ID,
		&account.Username,
		&passwordHash,
		&account.DisplayName,
		&account.Level,
		&account.ParentID,
		&account.Status,
		&permissions,
		&account.CreatedBy,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, "", err
	}
	account.Permissions = decodePermissions(permissions)
	account.CreatedAt = parseTime(createdAt)
	account.UpdatedAt = parseTime(updatedAt)
	return account, passwordHash, nil
}

func scanCard(row rowScanner) (*CardRecord, error) {
	card := &CardRecord{}
	var plan string
	var createdAt string
	var disabledAt sql.NullString
	if err := row.Scan(
		&card.ID,
		&card.CardHash,
		&plan,
		&card.Days,
		&card.MaxDevices,
		&card.Status,
		&card.Customer,
		&card.Remark,
		&card.OwnerAccountID,
		&card.CreatedByAccountID,
		&card.OwnerUsername,
		&createdAt,
		&disabledAt,
	); err != nil {
		return nil, err
	}
	card.Plan = Plan(plan)
	card.CreatedAt = parseTime(createdAt)
	if disabledAt.Valid {
		card.DisabledAt = parseTime(disabledAt.String)
	}
	return card, nil
}

func scanCardWithCount(row rowScanner) (*CardRecord, error) {
	card := &CardRecord{}
	var plan string
	var createdAt string
	var disabledAt sql.NullString
	if err := row.Scan(
		&card.ID,
		&card.CardHash,
		&plan,
		&card.Days,
		&card.MaxDevices,
		&card.Status,
		&card.Customer,
		&card.Remark,
		&card.OwnerAccountID,
		&card.CreatedByAccountID,
		&card.OwnerUsername,
		&createdAt,
		&disabledAt,
		&card.Activations,
	); err != nil {
		return nil, err
	}
	card.Plan = Plan(plan)
	card.CreatedAt = parseTime(createdAt)
	if disabledAt.Valid {
		card.DisabledAt = parseTime(disabledAt.String)
	}
	return card, nil
}

func scanActivation(row rowScanner) (*ActivationRecord, error) {
	activation := &ActivationRecord{}
	var plan string
	var activatedAt string
	var expiresAt string
	var lastCheckedAt string
	var disabledAt sql.NullString
	if err := row.Scan(
		&activation.ID,
		&activation.CardID,
		&activation.DeviceID,
		&activation.Status,
		&activatedAt,
		&expiresAt,
		&lastCheckedAt,
		&disabledAt,
		&activation.Platform,
		&activation.AppVersion,
		&activation.IPAddress,
		&activation.CardStatus,
		&plan,
		&activation.Days,
		&activation.Customer,
		&activation.Remark,
		&activation.OwnerAccountID,
		&activation.OwnerUsername,
	); err != nil {
		return nil, err
	}
	activation.ActivatedAt = parseTime(activatedAt)
	activation.Plan = Plan(plan)
	activation.ExpiresAt = parseTime(expiresAt)
	activation.LastCheckedAt = parseTime(lastCheckedAt)
	if disabledAt.Valid {
		activation.DisabledAt = parseTime(disabledAt.String)
	}
	return activation, nil
}

func scanAudit(row rowScanner) (*AuditRecord, error) {
	audit := &AuditRecord{}
	var createdAt string
	if err := row.Scan(&audit.ID, &audit.Action, &audit.TargetType, &audit.TargetID, &audit.Detail, &createdAt); err != nil {
		return nil, err
	}
	audit.CreatedAt = parseTime(createdAt)
	return audit, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse("2006-01-02 15:04:05", value); err == nil {
		return t.UTC()
	}
	return time.Time{}
}
