package storage

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	bearerTokenPattern      = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]+`)
	apiSecretValuePattern   = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|refresh[_-]?token|token|key)=([^&\s]+)`)
	jwtLikePattern          = regexp.MustCompile(`eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	openAISecretPattern     = regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`)
	urlQueryPattern         = regexp.MustCompile(`(https?://[^\s?]+)\?[^\s]+`)
	repeatedWhitespaceRegex = regexp.MustCompile(`\s+`)
)

const endpointErrorSampleMaxLen = 200

func SanitizeEndpointErrorSample(raw string) string {
	sample := strings.TrimSpace(raw)
	if sample == "" {
		return ""
	}
	sample = bearerTokenPattern.ReplaceAllString(sample, "Bearer [redacted]")
	sample = openAISecretPattern.ReplaceAllString(sample, "sk-[redacted]")
	sample = jwtLikePattern.ReplaceAllString(sample, "[redacted-jwt]")
	sample = urlQueryPattern.ReplaceAllString(sample, "$1?[redacted]")
	sample = apiSecretValuePattern.ReplaceAllString(sample, "$1=[redacted]")
	sample = repeatedWhitespaceRegex.ReplaceAllString(sample, " ")
	if len(sample) > endpointErrorSampleMaxLen {
		sample = sample[:endpointErrorSampleMaxLen]
	}
	return sample
}

func (s *SQLiteStorage) RecordEndpointErrorStat(record *EndpointErrorStatRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if record == nil {
		return fmt.Errorf("endpoint error stat is required")
	}
	normalized := normalizeEndpointErrorStat(*record)
	if normalized.EndpointName == "" {
		return fmt.Errorf("endpoint name is required")
	}
	if normalized.EndpointFingerprint == "" {
		normalized.EndpointFingerprint = normalized.EndpointName
	}
	if normalized.Reason == "" {
		normalized.Reason = "unknown"
	}
	if normalized.Count <= 0 {
		normalized.Count = 1
	}
	if normalized.WindowStart.IsZero() {
		normalized.WindowStart = normalized.LastAt.Truncate(5 * time.Minute)
	}
	if normalized.WindowEnd.IsZero() {
		normalized.WindowEnd = normalized.WindowStart.Add(5 * time.Minute)
	}
	if normalized.FirstAt.IsZero() {
		normalized.FirstAt = normalized.LastAt
	}
	if normalized.LastAt.IsZero() {
		normalized.LastAt = normalized.FirstAt
	}
	if normalized.FirstAt.IsZero() {
		normalized.FirstAt = time.Now().UTC()
		normalized.LastAt = normalized.FirstAt
	}
	normalized.Sample = SanitizeEndpointErrorSample(normalized.Sample)

	_, err := s.db.Exec(`
		INSERT INTO endpoint_error_stats (
			endpoint_name, endpoint_fingerprint, api_host, api_url_fingerprint, auth_mode,
			transformer, model, reason, status_code, window_start, window_end, first_at,
			last_at, count, uploaded_count, sample
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)
		ON CONFLICT(endpoint_fingerprint, reason, status_code, window_start) DO UPDATE SET
			endpoint_name=excluded.endpoint_name,
			api_host=excluded.api_host,
			api_url_fingerprint=excluded.api_url_fingerprint,
			auth_mode=excluded.auth_mode,
			transformer=excluded.transformer,
			model=excluded.model,
			window_end=excluded.window_end,
			first_at=MIN(endpoint_error_stats.first_at, excluded.first_at),
			last_at=MAX(endpoint_error_stats.last_at, excluded.last_at),
			count=endpoint_error_stats.count + excluded.count,
			sample=CASE WHEN excluded.sample != '' THEN excluded.sample ELSE endpoint_error_stats.sample END,
			updated_at=CURRENT_TIMESTAMP
	`, normalized.EndpointName, normalized.EndpointFingerprint, normalized.APIHost, normalized.APIURLFingerprint,
		normalized.AuthMode, normalized.Transformer, normalized.Model, normalized.Reason, normalized.StatusCode,
		normalized.WindowStart.UTC(), normalized.WindowEnd.UTC(), normalized.FirstAt.UTC(), normalized.LastAt.UTC(),
		normalized.Count, normalized.Sample)
	return err
}

func (s *SQLiteStorage) ListPendingEndpointErrorStats(limit int) ([]EndpointErrorStatRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.Query(`
		SELECT id, endpoint_name, endpoint_fingerprint, COALESCE(api_host,''), COALESCE(api_url_fingerprint,''),
			COALESCE(auth_mode,''), COALESCE(transformer,''), COALESCE(model,''), reason, status_code,
			window_start, window_end, first_at, last_at, count, uploaded_count, COALESCE(sample,'')
		FROM endpoint_error_stats
		WHERE count > uploaded_count
		ORDER BY window_start ASC, id ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]EndpointErrorStatRecord, 0)
	for rows.Next() {
		record, err := scanEndpointErrorStat(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *record)
	}
	return result, rows.Err()
}

func (s *SQLiteStorage) MarkEndpointErrorStatsUploaded(ids []int64, uploadedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(ids) == 0 {
		return nil
	}
	if uploadedAt.IsZero() {
		uploadedAt = time.Now()
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
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, err := tx.Exec(`
			UPDATE endpoint_error_stats
			SET uploaded_count=count, last_uploaded_at=?, updated_at=CURRENT_TIMESTAMP
			WHERE id=?
		`, uploadedAt.UTC(), id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func normalizeEndpointErrorStat(record EndpointErrorStatRecord) EndpointErrorStatRecord {
	record.EndpointName = strings.TrimSpace(record.EndpointName)
	record.EndpointFingerprint = strings.TrimSpace(record.EndpointFingerprint)
	record.APIHost = strings.TrimSpace(record.APIHost)
	record.APIURLFingerprint = strings.TrimSpace(record.APIURLFingerprint)
	record.AuthMode = strings.TrimSpace(record.AuthMode)
	record.Transformer = strings.TrimSpace(record.Transformer)
	record.Model = strings.TrimSpace(record.Model)
	record.Reason = strings.TrimSpace(record.Reason)
	return record
}

func scanEndpointErrorStat(scanner interface {
	Scan(dest ...interface{}) error
}) (*EndpointErrorStatRecord, error) {
	var record EndpointErrorStatRecord
	var windowStart, windowEnd, firstAt, lastAt sql.NullTime
	if err := scanner.Scan(
		&record.ID,
		&record.EndpointName,
		&record.EndpointFingerprint,
		&record.APIHost,
		&record.APIURLFingerprint,
		&record.AuthMode,
		&record.Transformer,
		&record.Model,
		&record.Reason,
		&record.StatusCode,
		&windowStart,
		&windowEnd,
		&firstAt,
		&lastAt,
		&record.Count,
		&record.UploadedCount,
		&record.Sample,
	); err != nil {
		return nil, err
	}
	if windowStart.Valid {
		record.WindowStart = windowStart.Time.UTC()
	}
	if windowEnd.Valid {
		record.WindowEnd = windowEnd.Time.UTC()
	}
	if firstAt.Valid {
		record.FirstAt = firstAt.Time.UTC()
	}
	if lastAt.Valid {
		record.LastAt = lastAt.Time.UTC()
	}
	return &record, nil
}
