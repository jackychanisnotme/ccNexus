package onlinelicense

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

	CREATE INDEX IF NOT EXISTS idx_license_cards_status ON license_cards(status);
	CREATE INDEX IF NOT EXISTS idx_license_cards_created_at ON license_cards(created_at);
	CREATE INDEX IF NOT EXISTS idx_license_activations_device ON license_activations(device_id);
	CREATE INDEX IF NOT EXISTS idx_license_activations_status ON license_activations(status);
	CREATE INDEX IF NOT EXISTS idx_license_activations_expires ON license_activations(expires_at);
	CREATE INDEX IF NOT EXISTS idx_admin_audit_logs_created_at ON admin_audit_logs(created_at);

	UPDATE license_activations
	SET expires_at = disabled_at, updated_at = CURRENT_TIMESTAMP
	WHERE status = 'disabled' AND disabled_at IS NOT NULL AND expires_at > disabled_at;
	`
	_, err := s.db.Exec(schema)
	return err
}

func (s *SQLiteStore) CreateCard(card *CardRecord) error {
	if card == nil {
		return fmt.Errorf("card is required")
	}
	result, err := s.db.Exec(`
		INSERT INTO license_cards (card_hash, plan, days, max_devices, status, customer, remark, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, card.CardHash, string(card.Plan), card.Days, card.MaxDevices, card.Status, card.Customer, card.Remark, formatTime(card.CreatedAt))
	if err != nil {
		return err
	}
	card.ID, err = result.LastInsertId()
	return err
}

func (s *SQLiteStore) FindCardByHash(hash string) (*CardRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, card_hash, plan, days, max_devices, status, COALESCE(customer, ''), COALESCE(remark, ''), created_at, disabled_at
		FROM license_cards WHERE card_hash = ?
	`, hash)
	return scanCard(row)
}

func (s *SQLiteStore) GetCard(id int64) (*CardRecord, error) {
	row := s.db.QueryRow(`
		SELECT c.id, c.card_hash, c.plan, c.days, c.max_devices, c.status, COALESCE(c.customer, ''), COALESCE(c.remark, ''), c.created_at, c.disabled_at,
			COUNT(a.id)
		FROM license_cards c
		LEFT JOIN license_activations a ON a.card_id = c.id AND a.status = ?
		WHERE c.id = ?
		GROUP BY c.id
	`, ActivationStatusActive, id)
	return scanCardWithCount(row)
}

func (s *SQLiteStore) ListCards() ([]CardRecord, error) {
	rows, err := s.db.Query(`
		SELECT c.id, c.card_hash, c.plan, c.days, c.max_devices, c.status, COALESCE(c.customer, ''), COALESCE(c.remark, ''), c.created_at, c.disabled_at,
			COUNT(a.id)
		FROM license_cards c
		LEFT JOIN license_activations a ON a.card_id = c.id AND a.status = ?
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
			COALESCE(c.customer, ''), COALESCE(c.remark, '')
		FROM license_activations a
		INNER JOIN license_cards c ON c.id = a.card_id
		WHERE a.card_id = ? AND a.device_id = ?
	`, cardID, deviceID)
	return scanActivation(row)
}

func (s *SQLiteStore) GetActivation(id int64) (*ActivationRecord, error) {
	row := s.db.QueryRow(`
		SELECT a.id, a.card_id, a.device_id, a.status, a.activated_at, a.expires_at, a.last_checked_at, a.disabled_at,
			COALESCE(a.platform, ''), COALESCE(a.app_version, ''), COALESCE(a.ip_address, ''),
			c.status, c.plan, c.days,
			COALESCE(c.customer, ''), COALESCE(c.remark, '')
		FROM license_activations a
		INNER JOIN license_cards c ON c.id = a.card_id
		WHERE a.id = ?
	`, id)
	return scanActivation(row)
}

func (s *SQLiteStore) LatestActivationForDevice(deviceID string) (*ActivationRecord, error) {
	row := s.db.QueryRow(`
		SELECT a.id, a.card_id, a.device_id, a.status, a.activated_at, a.expires_at, a.last_checked_at, a.disabled_at,
			COALESCE(a.platform, ''), COALESCE(a.app_version, ''), COALESCE(a.ip_address, ''),
			c.status, c.plan, c.days,
			COALESCE(c.customer, ''), COALESCE(c.remark, '')
		FROM license_activations a
		INNER JOIN license_cards c ON c.id = a.card_id
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
			COALESCE(c.customer, ''), COALESCE(c.remark, '')
		FROM license_activations a
		INNER JOIN license_cards c ON c.id = a.card_id
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
		SELECT id, card_hash, plan, days, max_devices, status, COALESCE(customer, ''), COALESCE(remark, ''), created_at, disabled_at
		FROM license_cards WHERE card_hash = ?
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
			COALESCE(c.customer, ''), COALESCE(c.remark, '')
		FROM license_activations a
		INNER JOIN license_cards c ON c.id = a.card_id
		WHERE a.card_id = ? AND a.device_id = ?
	`, cardID, deviceID)
	return scanActivation(row)
}

func latestActivationForDeviceTx(tx dbRunner, deviceID string) (*ActivationRecord, error) {
	row := tx.QueryRow(`
		SELECT a.id, a.card_id, a.device_id, a.status, a.activated_at, a.expires_at, a.last_checked_at, a.disabled_at,
			COALESCE(a.platform, ''), COALESCE(a.app_version, ''), COALESCE(a.ip_address, ''),
			c.status, c.plan, c.days,
			COALESCE(c.customer, ''), COALESCE(c.remark, '')
		FROM license_activations a
		INNER JOIN license_cards c ON c.id = a.card_id
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
			COALESCE(c.customer, ''), COALESCE(c.remark, '')
		FROM license_activations a
		INNER JOIN license_cards c ON c.id = a.card_id
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

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanCard(row rowScanner) (*CardRecord, error) {
	card := &CardRecord{}
	var plan string
	var createdAt string
	var disabledAt sql.NullString
	if err := row.Scan(&card.ID, &card.CardHash, &plan, &card.Days, &card.MaxDevices, &card.Status, &card.Customer, &card.Remark, &createdAt, &disabledAt); err != nil {
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
	if err := row.Scan(&card.ID, &card.CardHash, &plan, &card.Days, &card.MaxDevices, &card.Status, &card.Customer, &card.Remark, &createdAt, &disabledAt, &card.Activations); err != nil {
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
