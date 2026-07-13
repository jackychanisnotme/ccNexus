package onlinelicense

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

func (s *SQLiteStore) initAIStore() error {
	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS license_endpoint_usage_windows (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		device_id TEXT NOT NULL,
		owner_account_id INTEGER NOT NULL DEFAULT 0,
		endpoint_name TEXT NOT NULL DEFAULT '',
		endpoint_fingerprint TEXT NOT NULL,
		api_host TEXT,
		auth_mode TEXT,
		transformer TEXT,
		model TEXT,
		window_start DATETIME NOT NULL,
		window_end DATETIME NOT NULL,
		requests INTEGER NOT NULL DEFAULT 0,
		errors INTEGER NOT NULL DEFAULT 0,
		input_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL,
		UNIQUE(device_id, endpoint_fingerprint, window_start)
	);
	CREATE TABLE IF NOT EXISTS license_ai_settings (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		enabled INTEGER NOT NULL DEFAULT 0,
		model TEXT NOT NULL DEFAULT '',
		timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
		daily_time TEXT NOT NULL DEFAULT '02:30',
		monthly_time TEXT NOT NULL DEFAULT '09:00',
		prompt_version TEXT NOT NULL DEFAULT 'endpoint-stability-v1',
		updated_at DATETIME NOT NULL
	);
	CREATE TABLE IF NOT EXISTS license_ai_jobs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_type TEXT NOT NULL,
		owner_account_id INTEGER NOT NULL DEFAULT 0,
		period_start DATETIME NOT NULL,
		period_end DATETIME NOT NULL,
		prompt_version TEXT NOT NULL,
		status TEXT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,
		error TEXT,
		result_json TEXT,
		created_at DATETIME NOT NULL,
		started_at DATETIME,
		finished_at DATETIME,
		UNIQUE(job_type, owner_account_id, period_start, period_end, prompt_version)
	);
	CREATE TABLE IF NOT EXISTS license_ai_findings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id INTEGER NOT NULL,
		owner_account_id INTEGER NOT NULL DEFAULT 0,
		device_id TEXT,
		api_host TEXT,
		endpoint_name TEXT,
		classification TEXT NOT NULL,
		confidence REAL NOT NULL DEFAULT 0,
		severity TEXT NOT NULL DEFAULT 'info',
		count INTEGER NOT NULL DEFAULT 0,
		evidence_json TEXT,
		recommendation TEXT,
		customer_summary TEXT,
		created_at DATETIME NOT NULL
	);
	CREATE TABLE IF NOT EXISTS license_ai_reports (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id INTEGER NOT NULL DEFAULT 0,
		owner_account_id INTEGER NOT NULL DEFAULT 0,
		period_start DATETIME NOT NULL,
		period_end DATETIME NOT NULL,
		title TEXT NOT NULL,
		metrics_json TEXT NOT NULL DEFAULT '{}',
		narrative TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		UNIQUE(owner_account_id, period_start, period_end)
	);
	CREATE INDEX IF NOT EXISTS idx_license_usage_window_device ON license_endpoint_usage_windows(device_id, window_start);
	CREATE INDEX IF NOT EXISTS idx_license_usage_window_owner ON license_endpoint_usage_windows(owner_account_id, window_start);
	CREATE INDEX IF NOT EXISTS idx_license_usage_window_host ON license_endpoint_usage_windows(api_host, window_start);
	CREATE INDEX IF NOT EXISTS idx_license_ai_jobs_period ON license_ai_jobs(job_type, period_start, period_end);
	CREATE INDEX IF NOT EXISTS idx_license_ai_findings_scope ON license_ai_findings(owner_account_id, device_id, created_at);
	CREATE INDEX IF NOT EXISTS idx_license_ai_findings_host ON license_ai_findings(api_host, classification, created_at);
	CREATE INDEX IF NOT EXISTS idx_license_ai_reports_owner ON license_ai_reports(owner_account_id, period_start);
	INSERT OR IGNORE INTO license_ai_settings (
		id, enabled, model, timezone, daily_time, monthly_time, prompt_version, updated_at
	) VALUES (1, 0, '', 'Asia/Shanghai', '02:30', '09:00', 'endpoint-stability-v1', CURRENT_TIMESTAMP);
	`)
	return err
}

func recordSnapshotUsageTx(tx *sql.Tx, deviceID string, previous *RemoteSnapshot, next RemoteSnapshot, now time.Time) error {
	if previous == nil || previous.UpdatedAt.IsZero() || !now.After(previous.UpdatedAt) {
		return nil
	}
	ownerAccountID := int64(0)
	_ = tx.QueryRow(`SELECT owner_account_id FROM license_device_remote_state WHERE device_id=?`, deviceID).Scan(&ownerAccountID)
	oldByFingerprint := make(map[string]RemoteEndpointSnapshot, len(previous.Endpoints))
	for _, endpoint := range previous.Endpoints {
		oldByFingerprint[remoteEndpointFingerprint(endpoint)] = endpoint
	}
	for _, endpoint := range next.Endpoints {
		fingerprint := remoteEndpointFingerprint(endpoint)
		old, ok := oldByFingerprint[fingerprint]
		if !ok {
			continue
		}
		if endpoint.Stats.Requests < old.Stats.Requests ||
			endpoint.Stats.Errors < old.Stats.Errors ||
			endpoint.Stats.InputTokens < old.Stats.InputTokens ||
			endpoint.Stats.OutputTokens < old.Stats.OutputTokens {
			continue
		}
		requests := positiveCounterDelta(endpoint.Stats.Requests, old.Stats.Requests)
		errorCount := positiveCounterDelta(endpoint.Stats.Errors, old.Stats.Errors)
		inputTokens := positiveCounterDelta(endpoint.Stats.InputTokens, old.Stats.InputTokens)
		outputTokens := positiveCounterDelta(endpoint.Stats.OutputTokens, old.Stats.OutputTokens)
		if requests == 0 && errorCount == 0 && inputTokens == 0 && outputTokens == 0 {
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO license_endpoint_usage_windows (
				device_id, owner_account_id, endpoint_name, endpoint_fingerprint, api_host,
				auth_mode, transformer, model, window_start, window_end, requests, errors,
				input_tokens, output_tokens, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(device_id, endpoint_fingerprint, window_start) DO UPDATE SET
				window_end=MAX(license_endpoint_usage_windows.window_end, excluded.window_end),
				requests=MAX(license_endpoint_usage_windows.requests, excluded.requests),
				errors=MAX(license_endpoint_usage_windows.errors, excluded.errors),
				input_tokens=MAX(license_endpoint_usage_windows.input_tokens, excluded.input_tokens),
				output_tokens=MAX(license_endpoint_usage_windows.output_tokens, excluded.output_tokens)
		`, deviceID, ownerAccountID, strings.TrimSpace(endpoint.Name), fingerprint,
			remoteEndpointHost(endpoint.APIUrl), strings.TrimSpace(endpoint.AuthMode),
			strings.TrimSpace(endpoint.Transformer), strings.TrimSpace(endpoint.Model),
			formatTime(previous.UpdatedAt), formatTime(now), requests, errorCount, inputTokens,
			outputTokens, formatTime(now)); err != nil {
			return err
		}
	}
	_, _ = tx.Exec(`DELETE FROM license_endpoint_usage_windows WHERE window_end < ?`, formatTime(now.AddDate(0, 0, -90)))
	return nil
}

func positiveCounterDelta(current, previous int) int {
	if current <= previous {
		return 0
	}
	return current - previous
}

func remoteEndpointFingerprint(endpoint RemoteEndpointSnapshot) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(endpoint.Name),
		strings.TrimSpace(endpoint.APIUrl),
		strings.TrimSpace(endpoint.AuthMode),
		strings.TrimSpace(endpoint.Transformer),
		strings.TrimSpace(endpoint.Model),
	}, "\x00")))
	return hex.EncodeToString(sum[:])[:16]
}

func remoteEndpointHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return parsed.Host
}

func (s *SQLiteStore) ListEndpointUsageWindows(query AIQuery) ([]EndpointUsageRecord, error) {
	clauses, args := aiScopeWhere(query, "window_end", "window_start")
	sqlText := `
		SELECT id, device_id, owner_account_id, endpoint_name, endpoint_fingerprint,
			COALESCE(api_host,''), COALESCE(auth_mode,''), COALESCE(transformer,''),
			COALESCE(model,''), window_start, window_end, requests, errors,
			input_tokens, output_tokens
		FROM license_endpoint_usage_windows` + clauses + `
		ORDER BY window_start DESC, id DESC`
	rows, err := s.db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]EndpointUsageRecord, 0)
	for rows.Next() {
		var item EndpointUsageRecord
		var start, end sql.NullTime
		if err := rows.Scan(&item.ID, &item.DeviceID, &item.OwnerAccountID, &item.EndpointName,
			&item.EndpointFingerprint, &item.APIHost, &item.AuthMode, &item.Transformer,
			&item.Model, &start, &end, &item.Requests, &item.Errors, &item.InputTokens,
			&item.OutputTokens); err != nil {
			return nil, err
		}
		if start.Valid {
			item.WindowStart = start.Time.UTC()
		}
		if end.Valid {
			item.WindowEnd = end.Time.UTC()
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) ListEndpointErrorsForAI(query AIQuery) ([]EndpointErrorTelemetryRecord, error) {
	clauses, args := aiScopeWhere(query, "window_end", "window_start")
	sqlText := `
		SELECT id, device_id, activation_id, owner_account_id, COALESCE(platform,''), COALESCE(app_version,''),
			endpoint_name, endpoint_fingerprint, COALESCE(api_host,''), COALESCE(api_url_fingerprint,''),
			COALESCE(auth_mode,''), COALESCE(transformer,''), COALESCE(model,''), reason, status_code,
			count, first_at, last_at, window_start, window_end, COALESCE(sample,''), created_at, updated_at
		FROM license_endpoint_error_windows` + clauses + `
		ORDER BY window_start DESC, id DESC`
	rows, err := s.db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]EndpointErrorTelemetryRecord, 0)
	for rows.Next() {
		item, err := scanEndpointErrorTelemetryRecord(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *item)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) ListAIUsageOwnerIDs(from, to time.Time) ([]int64, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT owner_account_id
		FROM (
			SELECT owner_account_id FROM license_endpoint_usage_windows
			WHERE owner_account_id > 0 AND window_end >= ? AND window_start <= ?
			UNION
			SELECT owner_account_id FROM license_endpoint_error_windows
			WHERE owner_account_id > 0 AND window_end >= ? AND window_start <= ?
		)
		ORDER BY owner_account_id
	`, formatTime(from), formatTime(to), formatTime(from), formatTime(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]int64, 0)
	for rows.Next() {
		var ownerID int64
		if err := rows.Scan(&ownerID); err != nil {
			return nil, err
		}
		result = append(result, ownerID)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) CleanupAIData(now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := formatTime(now.AddDate(-1, 0, 0))
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM license_ai_findings WHERE created_at < ?`, cutoff); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM license_ai_reports WHERE created_at < ?`, cutoff); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM license_ai_jobs WHERE created_at < ?`, cutoff); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetAISettings() (AISettings, error) {
	var settings AISettings
	var enabled int
	var updated sql.NullTime
	err := s.db.QueryRow(`
		SELECT enabled, model, timezone, daily_time, monthly_time, prompt_version, updated_at
		FROM license_ai_settings WHERE id=1
	`).Scan(&enabled, &settings.Model, &settings.Timezone, &settings.DailyTime,
		&settings.MonthlyTime, &settings.PromptVersion, &updated)
	if err != nil {
		return settings, err
	}
	settings.Enabled = enabled != 0
	settings.GatewayURL = defaultAIGatewayURL
	if updated.Valid {
		settings.UpdatedAt = updated.Time.UTC()
	}
	return settings, nil
}

func (s *SQLiteStore) SaveAISettings(settings AISettings, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	_, err := s.db.Exec(`
		UPDATE license_ai_settings
		SET enabled=?, model=?, timezone=?, daily_time=?, monthly_time=?, prompt_version=?, updated_at=?
		WHERE id=1
	`, boolInt(settings.Enabled), strings.TrimSpace(settings.Model), strings.TrimSpace(settings.Timezone),
		strings.TrimSpace(settings.DailyTime), strings.TrimSpace(settings.MonthlyTime),
		strings.TrimSpace(settings.PromptVersion), formatTime(now))
	return err
}

func (s *SQLiteStore) CreateAIJob(job *AIJob) error {
	if job == nil {
		return fmt.Errorf("ai job is required")
	}
	result, err := s.db.Exec(`
		INSERT INTO license_ai_jobs (
			job_type, owner_account_id, period_start, period_end, prompt_version,
			status, attempts, error, result_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, job.JobType, job.OwnerAccountID, formatTime(job.PeriodStart), formatTime(job.PeriodEnd),
		job.PromptVersion, job.Status, job.Attempts, job.Error, string(job.Result), formatTime(job.CreatedAt))
	if err != nil {
		return err
	}
	job.ID, err = result.LastInsertId()
	return err
}

func (s *SQLiteStore) GetAIJob(id int64) (*AIJob, error) {
	row := s.db.QueryRow(`
		SELECT id, job_type, owner_account_id, period_start, period_end, prompt_version,
			status, attempts, COALESCE(error,''), COALESCE(result_json,''), created_at,
			started_at, finished_at
		FROM license_ai_jobs WHERE id=?
	`, id)
	return scanAIJob(row)
}

func (s *SQLiteStore) FindAIJob(jobType string, ownerAccountID int64, from, to time.Time, promptVersion string) (*AIJob, error) {
	row := s.db.QueryRow(`
		SELECT id, job_type, owner_account_id, period_start, period_end, prompt_version,
			status, attempts, COALESCE(error,''), COALESCE(result_json,''), created_at,
			started_at, finished_at
		FROM license_ai_jobs
		WHERE job_type=? AND owner_account_id=? AND period_start=? AND period_end=? AND prompt_version=?
	`, jobType, ownerAccountID, formatTime(from), formatTime(to), promptVersion)
	return scanAIJob(row)
}

func (s *SQLiteStore) UpdateAIJob(job *AIJob) error {
	if job == nil {
		return fmt.Errorf("ai job is required")
	}
	_, err := s.db.Exec(`
		UPDATE license_ai_jobs
		SET status=?, attempts=?, error=?, result_json=?, started_at=?, finished_at=?
		WHERE id=?
	`, job.Status, job.Attempts, job.Error, string(job.Result), nullableFormattedTime(job.StartedAt),
		nullableFormattedTime(job.FinishedAt), job.ID)
	return err
}

func (s *SQLiteStore) ListAIJobs(query AIQuery) ([]AIJob, error) {
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	clauses := []string{}
	args := []interface{}{}
	if query.OwnerAccountID > 0 {
		clauses = append(clauses, "owner_account_id=?")
		args = append(args, query.OwnerAccountID)
	}
	if !query.From.IsZero() {
		clauses = append(clauses, "period_end>=?")
		args = append(args, formatTime(query.From))
	}
	if !query.To.IsZero() {
		clauses = append(clauses, "period_start<=?")
		args = append(args, formatTime(query.To))
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT id, job_type, owner_account_id, period_start, period_end, prompt_version,
			status, attempts, COALESCE(error,''), COALESCE(result_json,''), created_at,
			started_at, finished_at
		FROM license_ai_jobs`+where+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AIJob, 0)
	for rows.Next() {
		item, err := scanAIJob(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *item)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) ReplaceAIFindings(jobID int64, findings []AIFinding, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM license_ai_findings WHERE job_id=?`, jobID); err != nil {
		return err
	}
	for _, finding := range findings {
		if _, err := tx.Exec(`
			INSERT INTO license_ai_findings (
				job_id, owner_account_id, device_id, api_host, endpoint_name, classification,
				confidence, severity, count, evidence_json, recommendation, customer_summary, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, jobID, finding.OwnerAccountID, finding.DeviceID, finding.APIHost, finding.EndpointName,
			finding.Classification, finding.Confidence, finding.Severity, finding.Count,
			string(finding.Evidence), finding.Recommendation, finding.CustomerSummary, formatTime(now)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) ListAIFindings(query AIQuery) ([]AIFinding, error) {
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	clauses := []string{}
	args := []interface{}{}
	if query.OwnerAccountID > 0 {
		clauses = append(clauses, "owner_account_id=?")
		args = append(args, query.OwnerAccountID)
	}
	if strings.TrimSpace(query.DeviceID) != "" {
		clauses = append(clauses, "device_id=?")
		args = append(args, strings.TrimSpace(query.DeviceID))
	}
	if strings.TrimSpace(query.APIHost) != "" {
		clauses = append(clauses, "api_host=?")
		args = append(args, strings.TrimSpace(query.APIHost))
	}
	if strings.TrimSpace(query.Classification) != "" {
		clauses = append(clauses, "classification=?")
		args = append(args, strings.TrimSpace(query.Classification))
	}
	if !query.From.IsZero() {
		clauses = append(clauses, "created_at>=?")
		args = append(args, formatTime(query.From))
	}
	if !query.To.IsZero() {
		clauses = append(clauses, "created_at<=?")
		args = append(args, formatTime(query.To))
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT id, job_id, owner_account_id, COALESCE(device_id,''), COALESCE(api_host,''),
			COALESCE(endpoint_name,''), classification, confidence, severity, count,
			COALESCE(evidence_json,''), COALESCE(recommendation,''), COALESCE(customer_summary,''),
			created_at
		FROM license_ai_findings`+where+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AIFinding, 0)
	for rows.Next() {
		var item AIFinding
		var evidence string
		var created sql.NullTime
		if err := rows.Scan(&item.ID, &item.JobID, &item.OwnerAccountID, &item.DeviceID,
			&item.APIHost, &item.EndpointName, &item.Classification, &item.Confidence,
			&item.Severity, &item.Count, &evidence, &item.Recommendation,
			&item.CustomerSummary, &created); err != nil {
			return nil, err
		}
		item.Evidence = json.RawMessage(evidence)
		if created.Valid {
			item.CreatedAt = created.Time.UTC()
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) SaveAIReport(report *AIReport) error {
	if report == nil {
		return fmt.Errorf("ai report is required")
	}
	result, err := s.db.Exec(`
		INSERT INTO license_ai_reports (
			job_id, owner_account_id, period_start, period_end, title, metrics_json,
			narrative, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_account_id, period_start, period_end) DO UPDATE SET
			job_id=excluded.job_id, title=excluded.title, metrics_json=excluded.metrics_json,
			narrative=excluded.narrative, created_at=excluded.created_at
	`, report.JobID, report.OwnerAccountID, formatTime(report.PeriodStart), formatTime(report.PeriodEnd),
		report.Title, string(report.Metrics), report.Narrative, formatTime(report.CreatedAt))
	if err != nil {
		return err
	}
	if report.ID == 0 {
		report.ID, _ = result.LastInsertId()
		if report.ID == 0 {
			existing, findErr := s.FindAIReport(report.OwnerAccountID, report.PeriodStart, report.PeriodEnd)
			if findErr == nil {
				report.ID = existing.ID
			}
		}
	}
	return nil
}

func (s *SQLiteStore) FindAIReport(ownerAccountID int64, from, to time.Time) (*AIReport, error) {
	row := s.db.QueryRow(`
		SELECT id, job_id, owner_account_id, period_start, period_end, title,
			metrics_json, narrative, created_at
		FROM license_ai_reports WHERE owner_account_id=? AND period_start=? AND period_end=?
	`, ownerAccountID, formatTime(from), formatTime(to))
	return scanAIReport(row)
}

func (s *SQLiteStore) GetAIReport(id int64) (*AIReport, error) {
	row := s.db.QueryRow(`
		SELECT id, job_id, owner_account_id, period_start, period_end, title,
			metrics_json, narrative, created_at
		FROM license_ai_reports WHERE id=?
	`, id)
	return scanAIReport(row)
}

func (s *SQLiteStore) ListAIReports(query AIQuery) ([]AIReport, error) {
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	clauses := []string{}
	args := []interface{}{}
	if query.OwnerAccountID > 0 {
		clauses = append(clauses, "owner_account_id=?")
		args = append(args, query.OwnerAccountID)
	}
	if !query.From.IsZero() {
		clauses = append(clauses, "period_end>=?")
		args = append(args, formatTime(query.From))
	}
	if !query.To.IsZero() {
		clauses = append(clauses, "period_start<=?")
		args = append(args, formatTime(query.To))
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT id, job_id, owner_account_id, period_start, period_end, title,
			metrics_json, narrative, created_at
		FROM license_ai_reports`+where+` ORDER BY period_end DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AIReport, 0)
	for rows.Next() {
		item, err := scanAIReport(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *item)
	}
	return result, rows.Err()
}

func aiScopeWhere(query AIQuery, endColumn, startColumn string) (string, []interface{}) {
	clauses := []string{}
	args := []interface{}{}
	if query.OwnerAccountID > 0 {
		clauses = append(clauses, "owner_account_id=?")
		args = append(args, query.OwnerAccountID)
	}
	if strings.TrimSpace(query.DeviceID) != "" {
		clauses = append(clauses, "device_id=?")
		args = append(args, strings.TrimSpace(query.DeviceID))
	}
	if strings.TrimSpace(query.APIHost) != "" {
		clauses = append(clauses, "api_host=?")
		args = append(args, strings.TrimSpace(query.APIHost))
	}
	if !query.From.IsZero() {
		clauses = append(clauses, endColumn+">=?")
		args = append(args, formatTime(query.From))
	}
	if !query.To.IsZero() {
		clauses = append(clauses, startColumn+"<=?")
		args = append(args, formatTime(query.To))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func scanAIJob(scanner interface{ Scan(...interface{}) error }) (*AIJob, error) {
	var job AIJob
	var result string
	var periodStart, periodEnd, created, started, finished sql.NullTime
	if err := scanner.Scan(&job.ID, &job.JobType, &job.OwnerAccountID, &periodStart, &periodEnd,
		&job.PromptVersion, &job.Status, &job.Attempts, &job.Error, &result, &created,
		&started, &finished); err != nil {
		return nil, err
	}
	job.Result = json.RawMessage(result)
	if periodStart.Valid {
		job.PeriodStart = periodStart.Time.UTC()
	}
	if periodEnd.Valid {
		job.PeriodEnd = periodEnd.Time.UTC()
	}
	if created.Valid {
		job.CreatedAt = created.Time.UTC()
	}
	if started.Valid {
		job.StartedAt = started.Time.UTC()
	}
	if finished.Valid {
		job.FinishedAt = finished.Time.UTC()
	}
	return &job, nil
}

func scanAIReport(scanner interface{ Scan(...interface{}) error }) (*AIReport, error) {
	var report AIReport
	var metrics string
	var start, end, created sql.NullTime
	if err := scanner.Scan(&report.ID, &report.JobID, &report.OwnerAccountID, &start, &end,
		&report.Title, &metrics, &report.Narrative, &created); err != nil {
		return nil, err
	}
	report.Metrics = json.RawMessage(metrics)
	if start.Valid {
		report.PeriodStart = start.Time.UTC()
	}
	if end.Valid {
		report.PeriodEnd = end.Time.UTC()
	}
	if created.Valid {
		report.CreatedAt = created.Time.UTC()
	}
	return &report, nil
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, sql.ErrNoRows) || strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
