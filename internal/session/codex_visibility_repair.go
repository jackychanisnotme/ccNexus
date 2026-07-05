package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
	_ "modernc.org/sqlite"
)

const (
	CodexVisibilityRepairModeQuick = "quick"
	CodexVisibilityRepairModeDeep  = "deep"

	CodexVisibilityRepairSessionScopeAll      = "all"
	CodexVisibilityRepairSessionScopeSelected = "selected"

	codexDefaultVisibilityProvider = "openai"
	codexOfficialStateDBFile       = "state_5.sqlite"
	codexVisibilityBackupSuffix    = "session-visibility-repair"
)

// CodexVisibilityRepairRequest controls the local Codex session visibility repair.
type CodexVisibilityRepairRequest struct {
	Mode         string   `json:"mode"`
	SessionScope string   `json:"sessionScope"`
	SessionIDs   []string `json:"sessionIds"`
}

// CodexVisibilityRepairSummary describes files and rows changed by the repair.
type CodexVisibilityRepairSummary struct {
	TargetProvider          string `json:"targetProvider"`
	UpdatedSqliteRowCount   int    `json:"updatedSqliteRowCount"`
	ChangedRolloutFileCount int    `json:"changedRolloutFileCount"`
	SkippedSqliteFileCount  int    `json:"skippedSqliteFileCount"`
	BackupDir               string `json:"backupDir"`
	Message                 string `json:"message"`
}

type codexVisibilityRepairPlan struct {
	dataDir        string
	targetProvider string
	mode           string
	sessionIDs     map[string]bool
	dbUpdates      []codexVisibilityDBUpdate
	rolloutUpdates []codexVisibilityRolloutUpdate
	skippedDBs     int
}

type codexVisibilityDBUpdate struct {
	path      string
	rowCount  int
	hasSchema codexVisibilityThreadsSchema
}

type codexVisibilityThreadsSchema struct {
	modelProvider bool
	hasUserEvent  bool
	firstUserMsg  bool
	threadSource  bool
	rolloutPath   bool
}

type codexVisibilityThreadRef struct {
	id          string
	rolloutPath string
}

type codexVisibilityRolloutUpdate struct {
	path    string
	content string
}

// RepairCodexSessionVisibility repairs the official Codex state records that
// control whether historical sessions appear in the Codex sidebar.
func RepairCodexSessionVisibility(request CodexVisibilityRepairRequest) (*CodexVisibilityRepairSummary, error) {
	plan, err := buildCodexVisibilityRepairPlan(request)
	if err != nil {
		return nil, err
	}
	summary := &CodexVisibilityRepairSummary{
		TargetProvider:          plan.targetProvider,
		SkippedSqliteFileCount:  plan.skippedDBs,
		UpdatedSqliteRowCount:   totalCodexVisibilityDBRows(plan.dbUpdates),
		ChangedRolloutFileCount: len(plan.rolloutUpdates),
	}
	if summary.UpdatedSqliteRowCount == 0 && summary.ChangedRolloutFileCount == 0 {
		summary.Message = fmt.Sprintf("Codex 会话可见性无需修复，当前 provider 为 %s。", plan.targetProvider)
		return summary, nil
	}

	backupDir, err := backupCodexVisibilityRepairFiles(plan)
	if err != nil {
		return nil, err
	}
	summary.BackupDir = backupDir
	if err := applyCodexVisibilityRepairPlan(plan); err != nil {
		restoreErr := restoreCodexVisibilityRepairBackup(plan.dataDir, backupDir)
		if restoreErr != nil {
			return nil, fmt.Errorf("修复 Codex 会话可见性失败: %w；自动回滚也失败: %v；备份目录: %s", err, restoreErr, backupDir)
		}
		return nil, fmt.Errorf("修复 Codex 会话可见性失败: %w；已自动回滚，备份目录: %s", err, backupDir)
	}

	summary.Message = fmt.Sprintf("Codex 会话可见性修复完成：更新 %d 行 state DB，修复 %d 个会话文件。",
		summary.UpdatedSqliteRowCount,
		summary.ChangedRolloutFileCount,
	)
	return summary, nil
}

func buildCodexVisibilityRepairPlan(request CodexVisibilityRepairRequest) (codexVisibilityRepairPlan, error) {
	dataDir := filepath.Dir(getCodexSessionsDir())
	mode := strings.TrimSpace(request.Mode)
	if mode == "" {
		mode = CodexVisibilityRepairModeQuick
	}
	if mode != CodexVisibilityRepairModeQuick && mode != CodexVisibilityRepairModeDeep {
		return codexVisibilityRepairPlan{}, fmt.Errorf("invalid codex visibility repair mode: %s", request.Mode)
	}
	sessionIDs := codexVisibilitySessionIDSet(request)
	targetProvider, err := readCodexVisibilityTargetProvider(dataDir)
	if err != nil {
		return codexVisibilityRepairPlan{}, err
	}
	plan := codexVisibilityRepairPlan{
		dataDir:        dataDir,
		targetProvider: targetProvider,
		mode:           mode,
		sessionIDs:     sessionIDs,
	}
	rolloutCandidates := make(map[string]struct{})
	for _, dbPath := range codexOfficialStateDBPaths(dataDir) {
		dbUpdate, refs, skipped, err := scanCodexVisibilityDB(dbPath, targetProvider, sessionIDs)
		if err != nil {
			return codexVisibilityRepairPlan{}, err
		}
		if skipped {
			plan.skippedDBs++
			continue
		}
		if dbUpdate.rowCount > 0 {
			plan.dbUpdates = append(plan.dbUpdates, dbUpdate)
		}
		for _, ref := range refs {
			if !codexVisibilityIncludesSession(sessionIDs, ref.id) {
				continue
			}
			path := resolveCodexVisibilityRolloutPath(dataDir, ref.rolloutPath)
			if path != "" {
				rolloutCandidates[path] = struct{}{}
			}
		}
	}
	for path := range rolloutCandidates {
		update, ok, err := buildCodexVisibilityRolloutUpdate(path, targetProvider, mode)
		if err != nil {
			return codexVisibilityRepairPlan{}, err
		}
		if ok {
			plan.rolloutUpdates = append(plan.rolloutUpdates, update)
		}
	}
	return plan, nil
}

func codexVisibilitySessionIDSet(request CodexVisibilityRepairRequest) map[string]bool {
	if strings.TrimSpace(request.SessionScope) != CodexVisibilityRepairSessionScopeSelected {
		return nil
	}
	ids := make(map[string]bool)
	for _, id := range request.SessionIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			ids[id] = true
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func codexVisibilityIncludesSession(sessionIDs map[string]bool, id string) bool {
	return len(sessionIDs) == 0 || sessionIDs[id]
}

func readCodexVisibilityTargetProvider(dataDir string) (string, error) {
	configPath := filepath.Join(dataDir, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return codexDefaultVisibilityProvider, nil
		}
		return "", fmt.Errorf("读取 Codex config.toml 失败: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return codexDefaultVisibilityProvider, nil
	}
	var cfg map[string]any
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("解析 Codex config.toml 失败: %w", err)
	}
	provider, _ := cfg["model_provider"].(string)
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return codexDefaultVisibilityProvider, nil
	}
	return provider, nil
}

func codexOfficialStateDBPaths(dataDir string) []string {
	paths := []string{
		filepath.Join(dataDir, "sqlite", codexOfficialStateDBFile),
		filepath.Join(dataDir, codexOfficialStateDBFile),
	}
	result := make([]string, 0, len(paths))
	seen := make(map[string]bool)
	for _, path := range paths {
		if !seen[path] {
			seen[path] = true
			result = append(result, path)
		}
	}
	return result
}

func scanCodexVisibilityDB(dbPath, targetProvider string, sessionIDs map[string]bool) (codexVisibilityDBUpdate, []codexVisibilityThreadRef, bool, error) {
	update := codexVisibilityDBUpdate{path: dbPath}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return update, nil, false, nil
		}
		return update, nil, false, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return update, nil, true, nil
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA busy_timeout = 3000"); err != nil {
		return update, nil, false, fmt.Errorf("设置 Codex state DB busy_timeout 失败 (%s): %w", dbPath, err)
	}
	schema, ok, err := readCodexVisibilityThreadsSchema(db)
	if err != nil {
		return update, nil, false, fmt.Errorf("读取 Codex state DB threads 表失败 (%s): %w", dbPath, err)
	}
	if !ok {
		return update, nil, false, nil
	}
	update.hasSchema = schema
	rowCount, err := countCodexVisibilityRowsToUpdate(db, schema, targetProvider, sessionIDs)
	if err != nil {
		return update, nil, false, fmt.Errorf("扫描 Codex state DB 待修复行失败 (%s): %w", dbPath, err)
	}
	update.rowCount = rowCount
	refs, err := readCodexVisibilityThreadRefs(db, schema, sessionIDs)
	if err != nil {
		return update, nil, false, fmt.Errorf("读取 Codex state DB rollout 引用失败 (%s): %w", dbPath, err)
	}
	return update, refs, false, nil
}

func readCodexVisibilityThreadsSchema(db *sql.DB) (codexVisibilityThreadsSchema, bool, error) {
	rows, err := db.Query("PRAGMA table_info(threads)")
	if err != nil {
		return codexVisibilityThreadsSchema{}, false, err
	}
	defer rows.Close()
	names := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return codexVisibilityThreadsSchema{}, false, err
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		return codexVisibilityThreadsSchema{}, false, err
	}
	if len(names) == 0 || !names["id"] {
		return codexVisibilityThreadsSchema{}, false, nil
	}
	return codexVisibilityThreadsSchema{
		modelProvider: names["model_provider"],
		hasUserEvent:  names["has_user_event"],
		firstUserMsg:  names["first_user_message"],
		threadSource:  names["thread_source"],
		rolloutPath:   names["rollout_path"],
	}, true, nil
}

func countCodexVisibilityRowsToUpdate(db *sql.DB, schema codexVisibilityThreadsSchema, targetProvider string, sessionIDs map[string]bool) (int, error) {
	whereClause := codexVisibilityWhereClause(schema)
	if whereClause == "" {
		return 0, nil
	}
	query := "SELECT COUNT(*) FROM threads WHERE (" + whereClause + ")"
	args := []any{}
	if schema.modelProvider {
		args = append(args, targetProvider)
	}
	if len(sessionIDs) > 0 {
		query += " AND id = ?"
		total := 0
		for id := range sessionIDs {
			rowArgs := append([]any{}, args...)
			rowArgs = append(rowArgs, id)
			var count int
			if err := db.QueryRow(query, rowArgs...).Scan(&count); err != nil {
				return 0, err
			}
			total += count
		}
		return total, nil
	}
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func readCodexVisibilityThreadRefs(db *sql.DB, schema codexVisibilityThreadsSchema, sessionIDs map[string]bool) ([]codexVisibilityThreadRef, error) {
	if !schema.rolloutPath {
		return nil, nil
	}
	query := "SELECT id, COALESCE(rollout_path, '') FROM threads WHERE COALESCE(rollout_path, '') <> ''"
	args := []any{}
	if len(sessionIDs) > 0 {
		query += " AND id = ?"
		refs := []codexVisibilityThreadRef{}
		for id := range sessionIDs {
			rows, err := db.Query(query, id)
			if err != nil {
				return nil, err
			}
			readRefs, err := scanCodexVisibilityThreadRefs(rows)
			if err != nil {
				return nil, err
			}
			refs = append(refs, readRefs...)
		}
		return refs, nil
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return scanCodexVisibilityThreadRefs(rows)
}

func scanCodexVisibilityThreadRefs(rows *sql.Rows) ([]codexVisibilityThreadRef, error) {
	defer rows.Close()
	var refs []codexVisibilityThreadRef
	for rows.Next() {
		var ref codexVisibilityThreadRef
		if err := rows.Scan(&ref.id, &ref.rolloutPath); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func codexVisibilityWhereClause(schema codexVisibilityThreadsSchema) string {
	var predicates []string
	if schema.modelProvider {
		predicates = append(predicates, "COALESCE(model_provider, '') <> ?")
	}
	if schema.hasUserEvent && schema.firstUserMsg {
		predicates = append(predicates, "(COALESCE(first_user_message, '') <> '' AND COALESCE(has_user_event, 0) <> 1)")
	}
	if schema.threadSource && schema.firstUserMsg {
		predicates = append(predicates, "(COALESCE(first_user_message, '') <> '' AND COALESCE(thread_source, '') = '')")
	}
	return strings.Join(predicates, " OR ")
}

func codexVisibilitySetClause(schema codexVisibilityThreadsSchema) string {
	var assignments []string
	if schema.modelProvider {
		assignments = append(assignments, "model_provider = ?")
	}
	if schema.hasUserEvent && schema.firstUserMsg {
		assignments = append(assignments, "has_user_event = CASE WHEN COALESCE(first_user_message, '') <> '' THEN 1 ELSE has_user_event END")
	}
	if schema.threadSource && schema.firstUserMsg {
		assignments = append(assignments, "thread_source = CASE WHEN COALESCE(thread_source, '') = '' AND COALESCE(first_user_message, '') <> '' THEN 'user' ELSE thread_source END")
	}
	return strings.Join(assignments, ", ")
}

func resolveCodexVisibilityRolloutPath(dataDir, rawPath string) string {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return ""
	}
	if filepath.IsAbs(rawPath) {
		return filepath.Clean(rawPath)
	}
	return filepath.Clean(filepath.Join(dataDir, rawPath))
}

func buildCodexVisibilityRolloutUpdate(path, targetProvider, mode string) (codexVisibilityRolloutUpdate, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return codexVisibilityRolloutUpdate{}, false, nil
		}
		return codexVisibilityRolloutUpdate{}, false, err
	}
	content := string(data)
	var updated string
	var changed bool
	if mode == CodexVisibilityRepairModeDeep {
		updated, changed, err = rewriteCodexVisibilityAllSessionMeta(content, targetProvider)
	} else {
		updated, changed, err = rewriteCodexVisibilityFirstSessionMeta(content, targetProvider)
	}
	if err != nil || !changed {
		return codexVisibilityRolloutUpdate{}, false, err
	}
	return codexVisibilityRolloutUpdate{path: path, content: updated}, true, nil
}

func rewriteCodexVisibilityFirstSessionMeta(content, targetProvider string) (string, bool, error) {
	lines := strings.Split(content, "\n")
	changed := false
	replaced := false
	for i, line := range lines {
		if !replaced {
			updated, ok, err := rewriteCodexVisibilitySessionMetaLine(line, targetProvider)
			if err != nil {
				return "", false, err
			}
			if ok {
				lines[i] = updated
				changed = true
				replaced = true
			}
		}
	}
	if !changed {
		return content, false, nil
	}
	return strings.Join(lines, "\n"), true, nil
}

func rewriteCodexVisibilityAllSessionMeta(content, targetProvider string) (string, bool, error) {
	lines := strings.Split(content, "\n")
	changed := false
	for i, line := range lines {
		updated, ok, err := rewriteCodexVisibilitySessionMetaLine(line, targetProvider)
		if err != nil {
			return "", false, err
		}
		if ok {
			lines[i] = updated
			changed = true
		}
	}
	if !changed {
		return content, false, nil
	}
	return strings.Join(lines, "\n"), true, nil
}

func rewriteCodexVisibilitySessionMetaLine(line, targetProvider string) (string, bool, error) {
	if strings.TrimSpace(line) == "" {
		return line, false, nil
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		return line, false, nil
	}
	if recordType, _ := record["type"].(string); recordType != "session_meta" {
		return line, false, nil
	}
	payload, _ := record["payload"].(map[string]any)
	if payload == nil {
		payload = make(map[string]any)
		record["payload"] = payload
	}
	if current, _ := payload["model_provider"].(string); current == targetProvider {
		return line, false, nil
	}
	payload["model_provider"] = targetProvider
	data, err := json.Marshal(record)
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

func totalCodexVisibilityDBRows(updates []codexVisibilityDBUpdate) int {
	total := 0
	for _, update := range updates {
		total += update.rowCount
	}
	return total
}

func backupCodexVisibilityRepairFiles(plan codexVisibilityRepairPlan) (string, error) {
	backupDir := filepath.Join(plan.dataDir, "backups", fmt.Sprintf("backup-%s-%s", time.Now().UTC().Format("20060102T150405Z"), codexVisibilityBackupSuffix))
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", err
	}
	for _, update := range plan.dbUpdates {
		if err := backupCodexVisibilityFile(plan.dataDir, backupDir, update.path); err != nil {
			return "", err
		}
		for _, sidecar := range codexVisibilitySQLiteSidecars(update.path) {
			if _, err := os.Stat(sidecar); err == nil {
				if err := backupCodexVisibilityFile(plan.dataDir, backupDir, sidecar); err != nil {
					return "", err
				}
			}
		}
	}
	for _, update := range plan.rolloutUpdates {
		if err := backupCodexVisibilityFile(plan.dataDir, backupDir, update.path); err != nil {
			return "", err
		}
	}
	return backupDir, nil
}

func backupCodexVisibilityFile(dataDir, backupDir, path string) error {
	rel, err := filepath.Rel(dataDir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(path)
	}
	target := filepath.Join(backupDir, rel)
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	return copyCodexVisibilityFile(path, target)
}

func copyCodexVisibilityFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func applyCodexVisibilityRepairPlan(plan codexVisibilityRepairPlan) error {
	for _, update := range plan.dbUpdates {
		if err := applyCodexVisibilityDBUpdate(update, plan.targetProvider, plan.sessionIDs); err != nil {
			return err
		}
	}
	for _, update := range plan.rolloutUpdates {
		if err := os.WriteFile(update.path, []byte(update.content), 0644); err != nil {
			return err
		}
	}
	return nil
}

func applyCodexVisibilityDBUpdate(update codexVisibilityDBUpdate, targetProvider string, sessionIDs map[string]bool) error {
	db, err := sql.Open("sqlite", update.path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA busy_timeout = 3000"); err != nil {
		return err
	}
	setClause := codexVisibilitySetClause(update.hasSchema)
	whereClause := codexVisibilityWhereClause(update.hasSchema)
	if setClause == "" || whereClause == "" {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	args := []any{}
	if update.hasSchema.modelProvider {
		args = append(args, targetProvider, targetProvider)
	}
	if len(sessionIDs) > 0 {
		sqlText := fmt.Sprintf("UPDATE threads SET %s WHERE (%s) AND id = ?", setClause, whereClause)
		for id := range sessionIDs {
			rowArgs := append([]any{}, args...)
			rowArgs = append(rowArgs, id)
			if _, err := tx.Exec(sqlText, rowArgs...); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	} else {
		sqlText := fmt.Sprintf("UPDATE threads SET %s WHERE %s", setClause, whereClause)
		if _, err := tx.Exec(sqlText, args...); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func restoreCodexVisibilityRepairBackup(dataDir, backupDir string) error {
	var restoreErr error
	err := filepath.WalkDir(backupDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			restoreErr = err
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(backupDir, path)
		if err != nil {
			restoreErr = err
			return nil
		}
		target := filepath.Join(dataDir, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			restoreErr = err
			return nil
		}
		if err := copyCodexVisibilityFile(path, target); err != nil {
			restoreErr = err
		}
		return nil
	})
	if err != nil {
		return err
	}
	return restoreErr
}

func codexVisibilitySQLiteSidecars(dbPath string) []string {
	return []string{dbPath + "-wal", dbPath + "-shm"}
}
