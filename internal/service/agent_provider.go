package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"gopkg.in/yaml.v3"
)

const agentProviderPlaceholderKey = "ccnexus-local"

var agentProviderTargets = []string{
	"claude",
	"claude_desktop",
	"codex",
	"gemini",
	"opencode",
	"openclaw",
	"hermes",
}

type AgentProviderOptions struct {
	HomeDir string
	DataDir string
}

type AgentProviderService struct {
	config  *config.Config
	homeDir string
	dataDir string
}

type AgentProviderRequest struct {
	Targets       []string `json:"targets"`
	CreateMissing bool     `json:"createMissing,omitempty"`
}

type AgentProviderRestoreRequest struct {
	BackupID string   `json:"backupId"`
	Targets  []string `json:"targets,omitempty"`
}

type AgentProviderStatus struct {
	TargetURL    string                       `json:"targetUrl"`
	Targets      []AgentProviderTargetStatus  `json:"targets"`
	LatestBackup *AgentProviderBackupSummary  `json:"latestBackup,omitempty"`
	Backups      []AgentProviderBackupSummary `json:"backups,omitempty"`
}

type AgentProviderTargetStatus struct {
	Target    string `json:"target"`
	Label     string `json:"label"`
	Path      string `json:"path"`
	Detected  bool   `json:"detected"`
	Available bool   `json:"available"`
}

type AgentProviderResult struct {
	TargetURL string                      `json:"targetUrl"`
	BackupID  string                      `json:"backupId,omitempty"`
	Results   []AgentProviderTargetResult `json:"results"`
}

type AgentProviderTargetResult struct {
	Target  string `json:"target"`
	Label   string `json:"label"`
	Path    string `json:"path,omitempty"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type AgentProviderBackupSummary struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	TargetURL string    `json:"targetUrl,omitempty"`
}

type agentProviderBackupManifest struct {
	ID        string                    `json:"id"`
	CreatedAt time.Time                 `json:"createdAt"`
	TargetURL string                    `json:"targetUrl"`
	Files     []agentProviderBackupFile `json:"files"`
}

type agentProviderBackupFile struct {
	Target     string `json:"target"`
	Path       string `json:"path"`
	BackupPath string `json:"backupPath"`
}

type agentProviderTarget struct {
	ID    string
	Label string
	Paths func(*AgentProviderService) []string
	Apply func(*AgentProviderService, string, bool) (string, string, string)
}

func NewAgentProviderService(cfg *config.Config) *AgentProviderService {
	homeDir, _ := os.UserHomeDir()
	dataDir := filepath.Join(homeDir, ".ccNexus")
	return NewAgentProviderServiceWithOptions(cfg, AgentProviderOptions{HomeDir: homeDir, DataDir: dataDir})
}

func NewAgentProviderServiceForDataDir(cfg *config.Config, dataDir string) *AgentProviderService {
	homeDir, _ := os.UserHomeDir()
	return NewAgentProviderServiceWithOptions(cfg, AgentProviderOptions{
		HomeDir: homeDir,
		DataDir: dataDir,
	})
}

func NewAgentProviderServiceWithOptions(cfg *config.Config, options AgentProviderOptions) *AgentProviderService {
	homeDir := strings.TrimSpace(options.HomeDir)
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	dataDir := strings.TrimSpace(options.DataDir)
	if dataDir == "" {
		dataDir = filepath.Join(homeDir, ".ccNexus")
	}
	return &AgentProviderService{config: cfg, homeDir: homeDir, dataDir: dataDir}
}

func (s *AgentProviderService) Status() AgentProviderStatus {
	status := AgentProviderStatus{TargetURL: s.targetURL()}
	for _, target := range s.knownTargets() {
		path, detected := s.firstExistingPath(target.Paths(s))
		status.Targets = append(status.Targets, AgentProviderTargetStatus{
			Target:    target.ID,
			Label:     target.Label,
			Path:      path,
			Detected:  detected,
			Available: detected,
		})
	}
	status.Backups = s.listBackups()
	if len(status.Backups) > 0 {
		status.LatestBackup = &status.Backups[0]
	}
	return status
}

func (s *AgentProviderService) StatusJSON() string {
	data, _ := json.Marshal(s.Status())
	return string(data)
}

func (s *AgentProviderService) ApplyFromJSON(targetsJSON string) string {
	var req AgentProviderRequest
	if strings.TrimSpace(targetsJSON) != "" {
		if err := json.Unmarshal([]byte(targetsJSON), &req); err != nil {
			data, _ := json.Marshal(AgentProviderResult{TargetURL: s.targetURL(), Results: []AgentProviderTargetResult{{
				Status: "failed", Message: fmt.Sprintf("invalid request: %v", err),
			}}})
			return string(data)
		}
	}
	data, _ := json.Marshal(s.Apply(req))
	return string(data)
}

func (s *AgentProviderService) RestoreFromJSON(backupID string, targetsJSON string) string {
	var req AgentProviderRestoreRequest
	req.BackupID = backupID
	if strings.TrimSpace(targetsJSON) != "" {
		if err := json.Unmarshal([]byte(targetsJSON), &req); err != nil {
			data, _ := json.Marshal(AgentProviderResult{TargetURL: s.targetURL(), Results: []AgentProviderTargetResult{{
				Status: "failed", Message: fmt.Sprintf("invalid request: %v", err),
			}}})
			return string(data)
		}
		if req.BackupID == "" {
			req.BackupID = backupID
		}
	}
	data, _ := json.Marshal(s.Restore(req))
	return string(data)
}

func (s *AgentProviderService) Apply(req AgentProviderRequest) AgentProviderResult {
	targetURL := s.targetURL()
	result := AgentProviderResult{TargetURL: targetURL}
	targets := s.selectTargets(req.Targets)
	if len(targets) == 0 {
		result.Results = append(result.Results, AgentProviderTargetResult{Status: "failed", Message: "no targets selected"})
		return result
	}

	var manifest *agentProviderBackupManifest
	for _, target := range targets {
		existingPaths := s.existingPaths(target.Paths(s))
		var backupFiles []agentProviderBackupFile
		backupFailed := false
		targetBackupID := ""
		if len(existingPaths) > 0 {
			if manifest == nil {
				manifest = s.newBackupManifest(targetURL)
			}
			targetBackupID = manifest.ID
			for _, existingPath := range existingPaths {
				backupPath, backupErr := s.backupFile(manifest.ID, target.ID, existingPath)
				if backupErr != nil {
					result.Results = append(result.Results, AgentProviderTargetResult{
						Target:  target.ID,
						Label:   target.Label,
						Path:    existingPath,
						Status:  "failed",
						Message: fmt.Sprintf("backup failed: %v", backupErr),
					})
					backupFailed = true
					break
				}
				backupFiles = append(backupFiles, agentProviderBackupFile{Target: target.ID, Path: existingPath, BackupPath: backupPath})
			}
		}
		if backupFailed {
			continue
		}
		if len(backupFiles) > 0 && targetBackupID != "" {
			manifest.Files = append(manifest.Files, backupFiles...)
		}
		path, status, message := target.Apply(s, targetURL, req.CreateMissing)
		item := AgentProviderTargetResult{Target: target.ID, Label: target.Label, Path: path, Status: status, Message: message}
		if status != "success" && len(backupFiles) > 0 {
			result.Results = append(result.Results, AgentProviderTargetResult{
				Target:  target.ID,
				Label:   target.Label,
				Path:    path,
				Status:  "failed",
				Message: fmt.Sprintf("apply failed after backup: %s", message),
			})
			continue
		}
		result.Results = append(result.Results, item)
	}
	if manifest != nil && len(manifest.Files) > 0 {
		if err := s.writeManifest(manifest); err == nil {
			result.BackupID = manifest.ID
		} else {
			result.Results = append(result.Results, AgentProviderTargetResult{Status: "failed", Message: fmt.Sprintf("manifest failed: %v", err)})
		}
	}
	return result
}

func (s *AgentProviderService) Restore(req AgentProviderRestoreRequest) AgentProviderResult {
	result := AgentProviderResult{TargetURL: s.targetURL(), BackupID: req.BackupID}
	manifest, err := s.readManifest(req.BackupID)
	if err != nil {
		result.Results = append(result.Results, AgentProviderTargetResult{Status: "failed", Message: err.Error()})
		return result
	}
	selected := s.targetSet(req.Targets)
	for _, file := range manifest.Files {
		if len(selected) > 0 && !selected[file.Target] {
			continue
		}
		target := s.targetByID(file.Target)
		label := file.Target
		if target != nil {
			label = target.Label
		}
		item := AgentProviderTargetResult{Target: file.Target, Label: label, Path: file.Path, Status: "restored"}
		data, err := os.ReadFile(file.BackupPath)
		if err != nil {
			item.Status = "failed"
			item.Message = err.Error()
			result.Results = append(result.Results, item)
			continue
		}
		if err := atomicWriteFile(file.Path, data, 0644); err != nil {
			item.Status = "failed"
			item.Message = err.Error()
		}
		result.Results = append(result.Results, item)
	}
	if len(result.Results) == 0 {
		result.Results = append(result.Results, AgentProviderTargetResult{Status: "skipped", Message: "no matching backup files"})
	}
	return result
}

func (s *AgentProviderService) ApplyAgentProviderConfig(targetsJSON string) string {
	return s.ApplyFromJSON(targetsJSON)
}

func (s *AgentProviderService) RestoreAgentProviderBackup(backupID string, targetsJSON string) string {
	return s.RestoreFromJSON(backupID, targetsJSON)
}

func (s *AgentProviderService) knownTargets() []agentProviderTarget {
	return []agentProviderTarget{
		{ID: "claude", Label: "Claude Code", Paths: func(s *AgentProviderService) []string {
			return []string{filepath.Join(s.homeDir, ".claude", "settings.json")}
		}, Apply: applyClaudeCode},
		{ID: "claude_desktop", Label: "Claude Desktop", Paths: func(s *AgentProviderService) []string {
			return []string{s.claudeDesktopProfilePath()}
		}, Apply: applyClaudeDesktop},
		{ID: "codex", Label: "Codex", Paths: func(s *AgentProviderService) []string {
			return []string{filepath.Join(s.homeDir, ".codex", "config.toml"), filepath.Join(s.homeDir, ".codex", "auth.json")}
		}, Apply: applyCodex},
		{ID: "gemini", Label: "Gemini CLI", Paths: func(s *AgentProviderService) []string {
			return []string{filepath.Join(s.homeDir, ".gemini", ".env")}
		}, Apply: applyGemini},
		{ID: "opencode", Label: "OpenCode", Paths: func(s *AgentProviderService) []string {
			return []string{filepath.Join(s.homeDir, ".config", "opencode", "opencode.json")}
		}, Apply: applyOpenCode},
		{ID: "openclaw", Label: "OpenClaw", Paths: func(s *AgentProviderService) []string {
			return []string{filepath.Join(s.homeDir, ".openclaw", "openclaw.json")}
		}, Apply: applyOpenClaw},
		{ID: "hermes", Label: "Hermes", Paths: func(s *AgentProviderService) []string {
			return []string{filepath.Join(s.homeDir, ".hermes", "config.yaml")}
		}, Apply: applyHermes},
	}
}

func (s *AgentProviderService) targetURL() string {
	port := 3000
	if s.config != nil && s.config.GetPort() > 0 {
		port = s.config.GetPort()
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func (s *AgentProviderService) selectTargets(ids []string) []agentProviderTarget {
	selected := s.targetSet(ids)
	all := s.knownTargets()
	if len(selected) == 0 {
		return all
	}
	var result []agentProviderTarget
	for _, target := range all {
		if selected[target.ID] {
			result = append(result, target)
		}
	}
	return result
}

func (s *AgentProviderService) targetSet(ids []string) map[string]bool {
	set := map[string]bool{}
	for _, id := range ids {
		normalized := normalizeAgentTargetID(id)
		if normalized != "" {
			set[normalized] = true
		}
	}
	return set
}

func normalizeAgentTargetID(id string) string {
	switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(id, "-", "_"))) {
	case "claude", "claude_code":
		return "claude"
	case "claude_desktop", "claudedesktop":
		return "claude_desktop"
	case "codex":
		return "codex"
	case "gemini", "gemini_cli":
		return "gemini"
	case "opencode", "open_code", "opencode_cli":
		return "opencode"
	case "openclaw", "open_claw", "openclaw_cli":
		return "openclaw"
	case "hermes":
		return "hermes"
	default:
		return ""
	}
}

func (s *AgentProviderService) targetByID(id string) *agentProviderTarget {
	normalized := normalizeAgentTargetID(id)
	for _, target := range s.knownTargets() {
		if target.ID == normalized {
			return &target
		}
	}
	return nil
}

func (s *AgentProviderService) firstExistingPath(paths []string) (string, bool) {
	for _, path := range paths {
		if fileExists(path) {
			return path, true
		}
	}
	if len(paths) > 0 {
		return paths[0], false
	}
	return "", false
}

func (s *AgentProviderService) existingPaths(paths []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, path := range paths {
		if !fileExists(path) || seen[path] {
			continue
		}
		seen[path] = true
		result = append(result, path)
	}
	return result
}

func (s *AgentProviderService) claudeDesktopProfilePath() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(s.homeDir, "Library", "Application Support", "Claude-3p", "configLibrary", "ccnexus.json")
	case "windows":
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			return filepath.Join(local, "Claude-3p", "configLibrary", "ccnexus.json")
		}
		return filepath.Join(s.homeDir, "AppData", "Local", "Claude-3p", "configLibrary", "ccnexus.json")
	default:
		return filepath.Join(s.homeDir, ".config", "Claude-3p", "configLibrary", "ccnexus.json")
	}
}

func applyClaudeCode(s *AgentProviderService, targetURL string, createMissing bool) (string, string, string) {
	path := filepath.Join(s.homeDir, ".claude", "settings.json")
	if !fileExists(path) && !createMissing {
		return path, "skipped", "configuration file not found"
	}
	var root map[string]any
	if err := readJSONFile(path, &root, map[string]any{}); err != nil {
		return path, "failed", err.Error()
	}
	env, _ := root["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	env["ANTHROPIC_BASE_URL"] = targetURL
	env["ANTHROPIC_API_KEY"] = agentProviderPlaceholderKey
	root["env"] = env
	if err := writeJSONFile(path, root); err != nil {
		return path, "failed", err.Error()
	}
	return path, "success", "updated"
}

func applyClaudeDesktop(s *AgentProviderService, targetURL string, createMissing bool) (string, string, string) {
	path := s.claudeDesktopProfilePath()
	if !fileExists(path) && !createMissing {
		return path, "skipped", "configuration file not found"
	}
	profile := map[string]any{
		"name": "ccNexus",
		"env": map[string]any{
			"ANTHROPIC_BASE_URL": targetURL,
			"ANTHROPIC_API_KEY":  agentProviderPlaceholderKey,
		},
	}
	if err := writeJSONFile(path, profile); err != nil {
		return path, "failed", err.Error()
	}
	return path, "success", "updated"
}

func applyCodex(s *AgentProviderService, targetURL string, createMissing bool) (string, string, string) {
	configPath := filepath.Join(s.homeDir, ".codex", "config.toml")
	authPath := filepath.Join(s.homeDir, ".codex", "auth.json")
	if !fileExists(configPath) && !fileExists(authPath) && !createMissing {
		return configPath, "skipped", "configuration file not found"
	}
	configText := fmt.Sprintf(`model_provider = "ccnexus"
model = "gpt-5"

[model_providers.ccnexus]
name = "ccNexus"
base_url = "%s/v1"
wire_api = "responses"
experimental_bearer_token = "%s"
`, targetURL, agentProviderPlaceholderKey)
	if err := atomicWriteFile(configPath, []byte(configText), 0644); err != nil {
		return configPath, "failed", err.Error()
	}
	auth := map[string]any{"OPENAI_API_KEY": agentProviderPlaceholderKey}
	if fileExists(authPath) {
		_ = readJSONFile(authPath, &auth, auth)
		auth["OPENAI_API_KEY"] = agentProviderPlaceholderKey
	}
	if err := writeJSONFile(authPath, auth); err != nil {
		return configPath, "failed", err.Error()
	}
	return configPath, "success", "updated"
}

func applyGemini(s *AgentProviderService, targetURL string, createMissing bool) (string, string, string) {
	path := filepath.Join(s.homeDir, ".gemini", ".env")
	if !fileExists(path) && !createMissing {
		return path, "skipped", "configuration file not found"
	}
	env := map[string]string{}
	if fileExists(path) {
		data, err := os.ReadFile(path)
		if err != nil {
			return path, "failed", err.Error()
		}
		env = parseEnv(string(data))
	}
	env["GOOGLE_GEMINI_BASE_URL"] = targetURL
	env["GEMINI_API_KEY"] = agentProviderPlaceholderKey
	if _, ok := env["GEMINI_MODEL"]; !ok {
		env["GEMINI_MODEL"] = "gemini-2.5-pro"
	}
	if err := atomicWriteFile(path, []byte(formatEnv(env)), 0600); err != nil {
		return path, "failed", err.Error()
	}
	return path, "success", "updated"
}

func applyOpenCode(s *AgentProviderService, targetURL string, createMissing bool) (string, string, string) {
	path := filepath.Join(s.homeDir, ".config", "opencode", "opencode.json")
	if !fileExists(path) && !createMissing {
		return path, "skipped", "configuration file not found"
	}
	root := map[string]any{"$schema": "https://opencode.ai/config.json"}
	if fileExists(path) {
		parsed, err := readLooseJSONFile(path)
		if err != nil {
			return path, "failed", err.Error()
		}
		root = parsed
	}
	providers, _ := root["provider"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	providers["ccnexus"] = map[string]any{
		"name": "ccNexus",
		"npm":  "@ai-sdk/openai-compatible",
		"options": map[string]any{
			"baseURL": targetURL + "/v1",
			"apiKey":  agentProviderPlaceholderKey,
		},
		"models": map[string]any{
			"gpt-5": map[string]any{},
		},
	}
	root["provider"] = providers
	if err := writeJSONFile(path, root); err != nil {
		return path, "failed", err.Error()
	}
	return path, "success", "updated"
}

func applyOpenClaw(s *AgentProviderService, targetURL string, createMissing bool) (string, string, string) {
	path := filepath.Join(s.homeDir, ".openclaw", "openclaw.json")
	if !fileExists(path) && !createMissing {
		return path, "skipped", "configuration file not found"
	}
	root := map[string]any{"models": map[string]any{"mode": "merge", "providers": map[string]any{}}}
	if fileExists(path) {
		parsed, err := readLooseJSONFile(path)
		if err != nil {
			return path, "failed", err.Error()
		}
		root = parsed
	}
	models, _ := root["models"].(map[string]any)
	if models == nil {
		models = map[string]any{}
	}
	providers, _ := models["providers"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	providers["ccnexus"] = map[string]any{
		"baseUrl": targetURL + "/v1",
		"apiKey":  agentProviderPlaceholderKey,
		"api":     "openai-completions",
		"models":  []any{map[string]any{"id": "gpt-5", "name": "gpt-5"}},
	}
	models["mode"] = "merge"
	models["providers"] = providers
	root["models"] = models
	agents, _ := root["agents"].(map[string]any)
	if agents == nil {
		agents = map[string]any{}
	}
	defaults, _ := agents["defaults"].(map[string]any)
	if defaults == nil {
		defaults = map[string]any{}
	}
	defaults["model"] = map[string]any{"primary": "ccnexus/gpt-5", "fallbacks": []any{}}
	agents["defaults"] = defaults
	root["agents"] = agents
	if err := writeJSONFile(path, root); err != nil {
		return path, "failed", err.Error()
	}
	return path, "success", "updated"
}

func applyHermes(s *AgentProviderService, targetURL string, createMissing bool) (string, string, string) {
	path := filepath.Join(s.homeDir, ".hermes", "config.yaml")
	if !fileExists(path) && !createMissing {
		return path, "skipped", "configuration file not found"
	}
	root := map[string]any{}
	if fileExists(path) {
		data, err := os.ReadFile(path)
		if err != nil {
			return path, "failed", err.Error()
		}
		if strings.TrimSpace(string(data)) != "" {
			if err := yaml.Unmarshal(data, &root); err != nil {
				return path, "failed", err.Error()
			}
		}
	}
	model, _ := root["model"].(map[string]any)
	if model == nil {
		model = map[string]any{}
	}
	model["provider"] = "ccnexus"
	model["base_url"] = targetURL
	if _, ok := model["default"]; !ok {
		model["default"] = "gpt-5"
	}
	root["model"] = model
	root["custom_providers"] = replaceHermesProvider(root["custom_providers"], map[string]any{
		"name":     "ccnexus",
		"base_url": targetURL + "/v1",
		"api_key":  agentProviderPlaceholderKey,
		"model":    "gpt-5",
	})
	data, err := yaml.Marshal(root)
	if err != nil {
		return path, "failed", err.Error()
	}
	if err := atomicWriteFile(path, data, 0644); err != nil {
		return path, "failed", err.Error()
	}
	return path, "success", "updated"
}

func replaceHermesProvider(existing any, provider map[string]any) []any {
	var providers []any
	if arr, ok := existing.([]any); ok {
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok && fmt.Sprint(m["name"]) == "ccnexus" {
				continue
			}
			providers = append(providers, item)
		}
	}
	return append(providers, provider)
}

func (s *AgentProviderService) newBackupManifest(targetURL string) *agentProviderBackupManifest {
	now := time.Now().UTC()
	return &agentProviderBackupManifest{
		ID:        now.Format("20060102-150405.000000000"),
		CreatedAt: now,
		TargetURL: targetURL,
	}
}

func (s *AgentProviderService) backupRoot() string {
	return filepath.Join(s.dataDir, "agent-provider-backups")
}

func (s *AgentProviderService) backupFile(backupID, target, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	backupPath := filepath.Join(s.backupRoot(), backupID, target+"-"+filepath.Base(path))
	if err := atomicWriteFile(backupPath, data, 0600); err != nil {
		return "", err
	}
	return backupPath, nil
}

func (s *AgentProviderService) writeManifest(manifest *agentProviderBackupManifest) error {
	return writeJSONFile(filepath.Join(s.backupRoot(), manifest.ID, "manifest.json"), manifest)
}

func (s *AgentProviderService) readManifest(backupID string) (*agentProviderBackupManifest, error) {
	if strings.TrimSpace(backupID) == "" {
		backups := s.listBackups()
		if len(backups) == 0 {
			return nil, fmt.Errorf("no backups available")
		}
		backupID = backups[0].ID
	}
	path := filepath.Join(s.backupRoot(), filepath.Base(backupID), "manifest.json")
	var manifest agentProviderBackupManifest
	if err := readJSONFile(path, &manifest, agentProviderBackupManifest{}); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (s *AgentProviderService) listBackups() []AgentProviderBackupSummary {
	entries, err := os.ReadDir(s.backupRoot())
	if err != nil {
		return nil
	}
	var backups []AgentProviderBackupSummary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifest, err := s.readManifest(entry.Name())
		if err != nil {
			continue
		}
		backups = append(backups, AgentProviderBackupSummary{ID: manifest.ID, CreatedAt: manifest.CreatedAt, TargetURL: manifest.TargetURL})
	}
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].CreatedAt.After(backups[j].CreatedAt)
	})
	return backups
}

func readJSONFile[T any](path string, out *T, defaultValue T) error {
	if !fileExists(path) {
		*out = defaultValue
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(data)) == "" {
		*out = defaultValue
		return nil
	}
	return json.Unmarshal(data, out)
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(path, data, 0644)
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func parseEnv(content string) map[string]string {
	env := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || !strings.Contains(trimmed, "=") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		key := strings.TrimSpace(parts[0])
		if key != "" {
			env[key] = strings.TrimSpace(parts[1])
		}
	}
	return env
}

func formatEnv(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var lines []string
	for _, key := range keys {
		lines = append(lines, key+"="+env[key])
	}
	return strings.Join(lines, "\n") + "\n"
}

func readLooseJSONFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err == nil {
		return root, nil
	}
	normalized := normalizeLooseJSON(string(data))
	if err := json.Unmarshal([]byte(normalized), &root); err != nil {
		return nil, err
	}
	return root, nil
}

func normalizeLooseJSON(input string) string {
	out := strings.TrimSpace(input)
	out = strings.ReplaceAll(out, "'", `"`)
	keyRE := regexp.MustCompile(`([,{]\s*)([A-Za-z_][A-Za-z0-9_-]*)(\s*:)`)
	for {
		next := keyRE.ReplaceAllString(out, `$1"$2"$3`)
		if next == out {
			break
		}
		out = next
	}
	trailingCommaRE := regexp.MustCompile(`,\s*([}\]])`)
	out = trailingCommaRE.ReplaceAllString(out, `$1`)
	return out
}
