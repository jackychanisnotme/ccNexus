package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/storage"
)

type AgentServiceOptions struct {
	LocalBaseURL string
	HTTPClient   *http.Client
	HomeDir      string
	DataDir      string
}

type AgentService struct {
	config        *config.Config
	proxy         *proxy.Proxy
	storage       *storage.SQLiteStorage
	endpoint      *EndpointService
	agentProvider *AgentProviderService
	inspector     *AgentProviderInspector
	httpClient    *http.Client
	localBaseURL  string
}

type AgentRunRequest struct {
	Task          string   `json:"task"`
	Tools         []string `json:"tools,omitempty"`
	RepairTargets []string `json:"repairTargets,omitempty"`
	MaxToolRounds int      `json:"maxToolRounds,omitempty"`
}

type AgentRunResult struct {
	Success         bool              `json:"success"`
	Answer          string            `json:"answer,omitempty"`
	EndpointURL     string            `json:"endpointUrl"`
	CurrentEndpoint string            `json:"currentEndpoint,omitempty"`
	Events          []AgentEvent      `json:"events"`
	ToolResults     []AgentToolResult `json:"toolResults"`
	Error           string            `json:"error,omitempty"`
}

type AgentEvent struct {
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

type AgentToolResult struct {
	Tool    string `json:"tool"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
	Data    any    `json:"data,omitempty"`
}

type AgentConfigRepairRequest struct {
	Targets       []string `json:"targets,omitempty"`
	CreateMissing bool     `json:"createMissing,omitempty"`
}

type AgentAppScanResult struct {
	Status  string                 `json:"status"`
	OS      string                 `json:"os"`
	Items   []AgentAppScanItem     `json:"items"`
	Scanned []AgentAppScannedScope `json:"scanned"`
}

type AgentAppScanItem struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	Source     string `json:"source"`
	Confidence string `json:"confidence"`
}

type AgentAppScannedScope struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

type agentAppSignature struct {
	ID       string
	Label    string
	Keywords []string
}

var agentAppSignatures = []agentAppSignature{
	{ID: "codex", Label: "Codex", Keywords: []string{"codex"}},
	{ID: "openclaw", Label: "OpenClaw", Keywords: []string{"openclaw", "open claw"}},
	{ID: "hermes", Label: "Hermes", Keywords: []string{"hermes"}},
	{ID: "cursor", Label: "Cursor", Keywords: []string{"cursor"}},
	{ID: "windsurf", Label: "Windsurf", Keywords: []string{"windsurf"}},
	{ID: "claude", Label: "Claude", Keywords: []string{"claude"}},
	{ID: "chatgpt", Label: "ChatGPT", Keywords: []string{"chatgpt", "openai"}},
	{ID: "copilot", Label: "Copilot", Keywords: []string{"copilot"}},
	{ID: "cline", Label: "Cline", Keywords: []string{"cline"}},
	{ID: "roo", Label: "Roo", Keywords: []string{"roo code", "roo"}},
	{ID: "continue", Label: "Continue", Keywords: []string{"continue"}},
	{ID: "ollama", Label: "Ollama", Keywords: []string{"ollama"}},
	{ID: "aider", Label: "Aider", Keywords: []string{"aider"}},
	{ID: "gemini", Label: "Gemini", Keywords: []string{"gemini"}},
	{ID: "qwen", Label: "Qwen", Keywords: []string{"qwen"}},
	{ID: "trae", Label: "Trae", Keywords: []string{"trae"}},
}

func NewAgentService(cfg *config.Config, p *proxy.Proxy, s *storage.SQLiteStorage, endpoint *EndpointService, provider *AgentProviderService) *AgentService {
	if provider == nil {
		provider = NewAgentProviderService(cfg)
	}
	agent := NewAgentServiceWithOptions(cfg, p, s, AgentServiceOptions{})
	agent.endpoint = endpoint
	agent.agentProvider = provider
	agent.inspector = &AgentProviderInspector{provider: provider}
	return agent
}

func NewAgentServiceWithOptions(cfg *config.Config, p *proxy.Proxy, s *storage.SQLiteStorage, options AgentServiceOptions) *AgentService {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	providerOptions := AgentProviderOptions{HomeDir: options.HomeDir, DataDir: options.DataDir}
	provider := NewAgentProviderServiceWithOptions(cfg, providerOptions)
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	return &AgentService{
		config:        cfg,
		proxy:         p,
		storage:       s,
		endpoint:      NewEndpointService(cfg, p, s),
		agentProvider: provider,
		inspector:     &AgentProviderInspector{provider: provider},
		httpClient:    client,
		localBaseURL:  strings.TrimRight(strings.TrimSpace(options.LocalBaseURL), "/"),
	}
}

func (s *AgentService) RunJSON(requestJSON string) string {
	var req AgentRunRequest
	if strings.TrimSpace(requestJSON) != "" {
		if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
			data, _ := json.Marshal(AgentRunResult{Success: false, Error: "invalid_request", Events: []AgentEvent{newAgentEvent("error", err.Error())}})
			return string(data)
		}
	}
	data, _ := json.Marshal(s.Run(req))
	return string(data)
}

func (s *AgentService) Run(req AgentRunRequest) AgentRunResult {
	result := AgentRunResult{EndpointURL: s.localProxyBaseURL(), CurrentEndpoint: s.currentEndpointName()}
	task := strings.TrimSpace(req.Task)
	result.Events = append(result.Events, newAgentEvent("preflight", "Validating agent task"))
	if task == "" {
		result.Error = "no_task"
		result.Events = append(result.Events, newAgentEvent("error", "Task is empty"))
		return result
	}
	if s.shouldRepair(req) {
		s.runRepairTools(req, &result)
	} else if s.shouldScanAgentApps(req) {
		s.runAgentAppScanTool(&result)
	} else if s.shouldInspectConfigs(req) {
		s.runInspectTools(req, &result)
	}
	if !s.hasEnabledEndpoints() {
		if s.hasLocalOnlyAnswer(&result) {
			result.Success = true
			result.Answer = localOnlyAgentAnswer(result.ToolResults)
			return result
		}
		result.Error = "no_enabled_endpoints"
		result.Events = append(result.Events, newAgentEvent("error", "No enabled endpoints are configured"))
		return result
	}

	answer, err := s.callResponses(task, result.ToolResults)
	if err != nil || strings.TrimSpace(answer) == "" {
		if err != nil {
			result.Events = append(result.Events, newAgentEvent("model_fallback", fmt.Sprintf("Responses call failed: %v", err)))
		} else {
			result.Events = append(result.Events, newAgentEvent("model_fallback", "Responses call returned an empty answer"))
		}
		answer, err = s.callChatCompletions(task, result.ToolResults)
	}
	if err != nil {
		result.Error = "model_call_failed"
		result.Events = append(result.Events, newAgentEvent("error", err.Error()))
		return result
	}
	result.Success = true
	result.Answer = answer
	result.Events = append(result.Events, newAgentEvent("model_success", "Model call completed through AINexus"))
	return result
}

func (s *AgentService) CheckAgentConfigs(req AgentProviderInspectRequest) AgentProviderInspectStatus {
	if s == nil || s.inspector == nil {
		return AgentProviderInspectStatus{Targets: []AgentProviderInspectTarget{{
			Status:       "failed",
			Problems:     []string{"agent inspector unavailable"},
			SuggestedFix: "Restart AINexus.",
		}}}
	}
	return s.inspector.Inspect(req)
}

func (s *AgentService) CheckAgentConfigsJSON(requestJSON string) string {
	var req AgentProviderInspectRequest
	if strings.TrimSpace(requestJSON) != "" {
		if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
			data, _ := json.Marshal(AgentProviderInspectStatus{Targets: []AgentProviderInspectTarget{{
				Status:       "failed",
				Problems:     []string{fmt.Sprintf("invalid request: %v", err)},
				SuggestedFix: "Send a JSON object with an optional targets array.",
			}}})
			return string(data)
		}
	}
	data, _ := json.Marshal(s.CheckAgentConfigs(req))
	return string(data)
}

func (s *AgentService) RepairAgentConfigs(req AgentConfigRepairRequest) AgentProviderResult {
	if s == nil || s.agentProvider == nil {
		return AgentProviderResult{Results: []AgentProviderTargetResult{{
			Status:  "failed",
			Message: "agent provider unavailable",
		}}}
	}
	return s.agentProvider.Apply(AgentProviderRequest{Targets: req.Targets, CreateMissing: req.CreateMissing})
}

func (s *AgentService) RepairAgentConfigsJSON(requestJSON string) string {
	var req AgentConfigRepairRequest
	if strings.TrimSpace(requestJSON) != "" {
		if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
			data, _ := json.Marshal(AgentProviderResult{Results: []AgentProviderTargetResult{{
				Status:  "failed",
				Message: fmt.Sprintf("invalid request: %v", err),
			}}})
			return string(data)
		}
	}
	data, _ := json.Marshal(s.RepairAgentConfigs(req))
	return string(data)
}

func (s *AgentService) ScanAgentApps() AgentAppScanResult {
	homeDir := s.agentHomeDir()
	result := AgentAppScanResult{Status: "ok", OS: runtime.GOOS}
	seen := map[string]bool{}
	for _, scope := range agentAppScanScopes(homeDir) {
		result.Scanned = append(result.Scanned, s.scanAgentScope(scope, seen, &result.Items))
	}
	sort.Slice(result.Items, func(i, j int) bool {
		if result.Items[i].Name == result.Items[j].Name {
			return result.Items[i].Path < result.Items[j].Path
		}
		return result.Items[i].Name < result.Items[j].Name
	})
	if len(result.Items) == 0 {
		result.Status = "empty"
	}
	return result
}

func (s *AgentService) agentHomeDir() string {
	if s != nil && s.agentProvider != nil && s.agentProvider.homeDir != "" {
		return s.agentProvider.homeDir
	}
	if homeDir, err := os.UserHomeDir(); err == nil {
		return homeDir
	}
	return ""
}

type agentAppScanScope struct {
	path string
	kind string
}

func agentAppScanScopes(homeDir string) []agentAppScanScope {
	scopes := []agentAppScanScope{}
	add := func(path, kind string) {
		if strings.TrimSpace(path) != "" && filepath.IsAbs(path) {
			scopes = append(scopes, agentAppScanScope{path: path, kind: kind})
		}
	}

	switch runtime.GOOS {
	case "darwin":
		add("/Applications", "applications")
		add(filepath.Join(homeDir, "Applications"), "applications")
		add(filepath.Join(homeDir, ".codex"), "config")
		add(filepath.Join(homeDir, ".openclaw"), "config")
		add(filepath.Join(homeDir, ".hermes"), "config")
		add(filepath.Join(homeDir, ".cursor"), "config")
		add(filepath.Join(homeDir, ".continue"), "config")
		add(filepath.Join(homeDir, ".ollama"), "config")
		add(filepath.Join(homeDir, "Library", "Application Support"), "app_support")
	case "windows":
		appData := os.Getenv("APPDATA")
		localAppData := os.Getenv("LOCALAPPDATA")
		programFiles := os.Getenv("ProgramFiles")
		programFilesX86 := os.Getenv("ProgramFiles(x86)")
		add(filepath.Join(homeDir, "AppData", "Roaming"), "app_data")
		add(filepath.Join(homeDir, "AppData", "Local"), "app_data")
		add(appData, "app_data")
		add(localAppData, "app_data")
		add(programFiles, "program_files")
		add(programFilesX86, "program_files")
		add(filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs"), "start_menu")
		add(filepath.Join(homeDir, ".codex"), "config")
		add(filepath.Join(homeDir, ".openclaw"), "config")
		add(filepath.Join(homeDir, ".hermes"), "config")
	default:
		add(filepath.Join(homeDir, ".local", "share", "applications"), "desktop_entries")
		add("/usr/share/applications", "desktop_entries")
		add(filepath.Join(homeDir, ".config"), "config")
		add(filepath.Join(homeDir, ".local", "share"), "app_data")
		add(filepath.Join(homeDir, ".codex"), "config")
		add(filepath.Join(homeDir, ".openclaw"), "config")
		add(filepath.Join(homeDir, ".hermes"), "config")
	}
	return scopes
}

func (s *AgentService) scanAgentScope(scope agentAppScanScope, seen map[string]bool, items *[]AgentAppScanItem) AgentAppScannedScope {
	status := AgentAppScannedScope{Path: scope.path, Kind: scope.kind, Status: "missing"}
	info, err := os.Stat(scope.path)
	if err != nil {
		if !os.IsNotExist(err) {
			status.Status = "unreadable"
		}
		return status
	}
	status.Status = "scanned"
	if !info.IsDir() {
		if item, ok := agentAppItemFromPath(scope.path, scope.kind); ok {
			addAgentAppScanItem(items, seen, item)
		}
		return status
	}

	if item, ok := agentAppItemFromPath(scope.path, scope.kind); ok && scope.kind == "config" {
		addAgentAppScanItem(items, seen, item)
	}
	entries, err := os.ReadDir(scope.path)
	if err != nil {
		status.Status = "unreadable"
		return status
	}
	for _, entry := range entries {
		entryPath := filepath.Join(scope.path, entry.Name())
		if item, ok := agentAppItemFromPath(entryPath, scope.kind); ok {
			addAgentAppScanItem(items, seen, item)
		}
	}
	return status
}

func agentAppItemFromPath(path, source string) (AgentAppScanItem, bool) {
	name := filepath.Base(path)
	normalized := normalizeAgentAppName(name)
	for _, signature := range agentAppSignatures {
		for _, keyword := range signature.Keywords {
			if strings.Contains(normalized, normalizeAgentAppName(keyword)) {
				return AgentAppScanItem{
					Name:       signature.Label,
					Kind:       agentAppKind(path, source),
					Path:       path,
					Source:     source,
					Confidence: "name_match",
				}, true
			}
		}
	}
	return AgentAppScanItem{}, false
}

func normalizeAgentAppName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, suffix := range []string{".app", ".appimage", ".desktop", ".lnk", ".exe"} {
		value = strings.TrimSuffix(value, suffix)
	}
	value = strings.ReplaceAll(value, "-", " ")
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.Join(strings.Fields(value), " ")
	return value
}

func agentAppKind(path, source string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.Contains(source, "config") || strings.HasPrefix(filepath.Base(path), "."):
		return "config"
	case strings.HasSuffix(lower, ".app") || strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".appimage"):
		return "application"
	case strings.HasSuffix(lower, ".desktop") || strings.HasSuffix(lower, ".lnk"):
		return "shortcut"
	default:
		return "directory"
	}
}

func addAgentAppScanItem(items *[]AgentAppScanItem, seen map[string]bool, item AgentAppScanItem) {
	key := strings.ToLower(item.Name + "\x00" + item.Kind + "\x00" + item.Path)
	if seen[key] {
		return
	}
	seen[key] = true
	*items = append(*items, item)
}

func (s *AgentService) callResponses(task string, toolResults []AgentToolResult) (string, error) {
	payload := map[string]any{
		"model":        "gpt-5",
		"instructions": agentSystemPrompt(),
		"input": []map[string]any{
			{
				"type": "message",
				"role": "user",
				"content": []map[string]any{{
					"type": "input_text",
					"text": buildAgentUserPrompt(task, toolResults),
				}},
			},
		},
		"max_output_tokens": 800,
	}
	data, err := s.postLocalJSON("/v1/responses", payload)
	if err != nil {
		return "", err
	}
	answer := parseResponsesAnswer(data)
	if strings.TrimSpace(answer) == "" {
		return "", fmt.Errorf("empty responses answer")
	}
	return answer, nil
}

func (s *AgentService) callChatCompletions(task string, toolResults []AgentToolResult) (string, error) {
	payload := map[string]any{
		"model": "gpt-5",
		"messages": []map[string]any{
			{"role": "system", "content": agentSystemPrompt()},
			{"role": "user", "content": buildAgentUserPrompt(task, toolResults)},
		},
		"max_tokens": 800,
	}
	data, err := s.postLocalJSON("/v1/chat/completions", payload)
	if err != nil {
		return "", err
	}
	answer := parseChatAnswer(data)
	if strings.TrimSpace(answer) == "" {
		return "", fmt.Errorf("empty chat answer")
	}
	return answer, nil
}

func (s *AgentService) postLocalJSON(path string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, s.localProxyBaseURL()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+agentProviderPlaceholderKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(proxy.AgentNoEndpointThinkingHeader, "1")
	client := s.httpClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("local proxy returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func parseResponsesAnswer(data []byte) string {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return ""
	}
	if text := strings.TrimSpace(fmt.Sprint(root["output_text"])); text != "" && text != "<nil>" {
		return text
	}
	if output, ok := root["output"].([]any); ok {
		for _, item := range output {
			itemMap, _ := item.(map[string]any)
			content, _ := itemMap["content"].([]any)
			for _, block := range content {
				blockMap, _ := block.(map[string]any)
				if text := strings.TrimSpace(fmt.Sprint(blockMap["text"])); text != "" && text != "<nil>" {
					return text
				}
			}
		}
	}
	return parseChatAnswer(data)
}

func parseChatAnswer(data []byte) string {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return ""
	}
	choices, _ := root["choices"].([]any)
	if len(choices) == 0 {
		return ""
	}
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	if text := strings.TrimSpace(fmt.Sprint(message["content"])); text != "" && text != "<nil>" {
		return text
	}
	return ""
}

func (s *AgentService) localProxyBaseURL() string {
	if s != nil && s.localBaseURL != "" {
		return s.localBaseURL
	}
	port := 3000
	if s != nil && s.config != nil && s.config.GetPort() > 0 {
		port = s.config.GetPort()
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func (s *AgentService) hasEnabledEndpoints() bool {
	if s == nil || s.config == nil {
		return false
	}
	for _, endpoint := range s.config.GetEndpoints() {
		if endpoint.Enabled {
			return true
		}
	}
	return false
}

func (s *AgentService) currentEndpointName() string {
	if s != nil && s.proxy != nil {
		return s.proxy.GetCurrentEndpointName()
	}
	if s != nil && s.config != nil {
		for _, endpoint := range s.config.GetEndpoints() {
			if endpoint.Enabled {
				return endpoint.Name
			}
		}
	}
	return ""
}

func agentSystemPrompt() string {
	return "You are the built-in AINexus agent. Answer the user's task using the supplied endpoint and tool context. You cannot run arbitrary shell commands or edit project files. You may summarize controlled AINexus read-only and repair tool results."
}

func buildAgentUserPrompt(task string, toolResults []AgentToolResult) string {
	if len(toolResults) == 0 {
		return task
	}
	data, _ := json.Marshal(toolResults)
	return fmt.Sprintf("%s\n\nAINexus tool results:\n%s", task, string(data))
}

func newAgentEvent(eventType, message string) AgentEvent {
	return AgentEvent{Type: eventType, Message: message, CreatedAt: time.Now().UTC()}
}

func (s *AgentService) shouldRepair(req AgentRunRequest) bool {
	if len(req.RepairTargets) > 0 {
		return true
	}
	task := strings.ToLower(req.Task)
	for _, marker := range []string{"repair", "fix", "修复", "修理", "修正"} {
		if strings.Contains(task, marker) {
			return true
		}
	}
	return false
}

func (s *AgentService) shouldInspectConfigs(req AgentRunRequest) bool {
	task := strings.ToLower(req.Task)
	normalized := normalizeAgentIntentText(task)
	hasChineseInspectVerb := strings.Contains(task, "检查") || strings.Contains(task, "检测") || strings.Contains(task, "查看") || strings.Contains(task, "诊断") || strings.Contains(task, "健康") || strings.Contains(task, "状态")
	hasEnglishInspectVerb := strings.Contains(task, "check") || strings.Contains(task, "inspect") || strings.Contains(task, "diagnose") || strings.Contains(task, "validate") || strings.Contains(task, "health") || strings.Contains(task, "status")
	hasConfigSubject := strings.Contains(task, "配置") || strings.Contains(task, "config") || strings.Contains(task, "setup") || strings.Contains(task, "健康") || strings.Contains(task, "状态") || strings.Contains(task, "health") || strings.Contains(task, "status")
	hasAgentSubject := strings.Contains(normalized, "agent") || strings.Contains(normalized, "claude") || strings.Contains(normalized, "codex") || strings.Contains(normalized, "openclaw") || strings.Contains(normalized, "hermes")
	chineseInspect := hasChineseInspectVerb && (hasConfigSubject || hasAgentSubject)
	englishInspect := hasEnglishInspectVerb && (hasConfigSubject || hasAgentSubject)
	return chineseInspect || englishInspect
}

func (s *AgentService) shouldScanAgentApps(req AgentRunRequest) bool {
	task := strings.ToLower(req.Task)
	normalized := normalizeAgentIntentText(task)
	hasScanVerb := strings.Contains(task, "扫描") || strings.Contains(task, "scan") || strings.Contains(task, "查找") || strings.Contains(task, "找一下") || strings.Contains(task, "有什么") || strings.Contains(task, "有哪些")
	hasComputerSubject := strings.Contains(task, "电脑") || strings.Contains(task, "本机") || strings.Contains(task, "系统") || strings.Contains(task, "local") || strings.Contains(task, "computer") || strings.Contains(task, "machine")
	hasAgentSubject := strings.Contains(normalized, "agent") || strings.Contains(task, "ai app") || strings.Contains(task, "ai工具") || strings.Contains(task, "ai 工具") || strings.Contains(normalized, "codex") || strings.Contains(normalized, "cursor") || strings.Contains(normalized, "claude") || strings.Contains(normalized, "chatgpt")
	return hasScanVerb && (hasComputerSubject || hasAgentSubject) && hasAgentSubject
}

func normalizeAgentIntentText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(" ", "", "-", "", "_", "", ".", "")
	return replacer.Replace(value)
}

func agentTargetsFromTask(task string) []string {
	normalized := normalizeAgentIntentText(task)
	targets := []string{}
	if strings.Contains(normalized, "claude") {
		targets = append(targets, "claude")
	}
	if strings.Contains(normalized, "codex") {
		targets = append(targets, "codex")
	}
	if strings.Contains(normalized, "openclaw") {
		targets = append(targets, "openclaw")
	}
	if strings.Contains(normalized, "hermes") {
		targets = append(targets, "hermes")
	}
	if len(targets) == 0 {
		return []string{"codex", "openclaw", "hermes"}
	}
	return targets
}

func (s *AgentService) runAgentAppScanTool(result *AgentRunResult) {
	result.Events = append(result.Events, newAgentEvent("tool", "Scanning local agent apps read-only"))
	scan := s.ScanAgentApps()
	result.ToolResults = append(result.ToolResults, AgentToolResult{
		Tool:    "scan_agent_apps",
		Status:  scan.Status,
		Summary: summarizeAgentAppScan(scan),
		Data:    scan,
	})
}

func (s *AgentService) runInspectTools(req AgentRunRequest, result *AgentRunResult) {
	targets := req.RepairTargets
	if len(targets) == 0 {
		targets = agentTargetsFromTask(req.Task)
	}
	result.Events = append(result.Events, newAgentEvent("tool", "Checking agent configs"))
	status := s.CheckAgentConfigs(AgentProviderInspectRequest{Targets: targets})
	result.ToolResults = append(result.ToolResults, AgentToolResult{
		Tool:    "check_agent_configs",
		Status:  inspectOverallStatus(status),
		Summary: summarizeInspectStatus(status),
		Data:    status,
	})
}

func (s *AgentService) hasLocalOnlyAnswer(result *AgentRunResult) bool {
	if result == nil {
		return false
	}
	for _, tool := range result.ToolResults {
		if tool.Tool == "repair_agent_configs" {
			return false
		}
	}
	for _, tool := range result.ToolResults {
		if tool.Tool == "scan_agent_apps" || tool.Tool == "check_agent_configs" {
			return true
		}
	}
	return false
}

func localOnlyAgentAnswer(toolResults []AgentToolResult) string {
	for _, tool := range toolResults {
		if tool.Tool != "scan_agent_apps" {
			if tool.Tool == "check_agent_configs" {
				if status, ok := tool.Data.(AgentProviderInspectStatus); ok {
					return renderAgentConfigHealthAnswer(status)
				}
				data, _ := json.Marshal(tool.Data)
				var status AgentProviderInspectStatus
				if err := json.Unmarshal(data, &status); err == nil {
					return renderAgentConfigHealthAnswer(status)
				}
			}
			continue
		}
		if scan, ok := tool.Data.(AgentAppScanResult); ok {
			return renderAgentAppScanAnswer(scan)
		}
		data, _ := json.Marshal(tool.Data)
		var scan AgentAppScanResult
		if err := json.Unmarshal(data, &scan); err == nil {
			return renderAgentAppScanAnswer(scan)
		}
	}
	return "只读本机扫描已完成。"
}

func (s *AgentService) runRepairTools(req AgentRunRequest, result *AgentRunResult) {
	targets := req.RepairTargets
	if len(targets) == 0 {
		targets = []string{"codex", "openclaw", "hermes"}
	}
	result.Events = append(result.Events, newAgentEvent("tool", "Checking agent configs before repair"))
	before := s.CheckAgentConfigs(AgentProviderInspectRequest{Targets: targets})
	result.ToolResults = append(result.ToolResults, AgentToolResult{
		Tool:    "check_agent_configs",
		Status:  inspectOverallStatus(before),
		Summary: summarizeInspectStatus(before),
		Data:    before,
	})
	result.Events = append(result.Events, newAgentEvent("tool", "Repairing selected agent configs"))
	repair := s.RepairAgentConfigs(AgentConfigRepairRequest{Targets: targets, CreateMissing: true})
	result.ToolResults = append(result.ToolResults, AgentToolResult{
		Tool:    "repair_agent_configs",
		Status:  repairOverallStatus(repair),
		Summary: summarizeRepairResult(repair),
		Data:    repair,
	})
	result.Events = append(result.Events, newAgentEvent("tool", "Checking agent configs after repair"))
	after := s.CheckAgentConfigs(AgentProviderInspectRequest{Targets: targets})
	result.ToolResults = append(result.ToolResults, AgentToolResult{
		Tool:    "check_agent_configs",
		Status:  inspectOverallStatus(after),
		Summary: summarizeInspectStatus(after),
		Data:    after,
	})
}

func inspectOverallStatus(status AgentProviderInspectStatus) string {
	if len(status.Targets) == 0 {
		return "skipped"
	}
	for _, target := range status.Targets {
		if !target.Healthy {
			return "unhealthy"
		}
	}
	return "healthy"
}

func repairOverallStatus(result AgentProviderResult) string {
	if len(result.Results) == 0 {
		return "skipped"
	}
	for _, item := range result.Results {
		if item.Status == "failed" {
			return "failed"
		}
	}
	return "success"
}

func summarizeInspectStatus(status AgentProviderInspectStatus) string {
	healthy := 0
	for _, target := range status.Targets {
		if target.Healthy {
			healthy++
		}
	}
	return fmt.Sprintf("%d/%d agent configs healthy", healthy, len(status.Targets))
}

func summarizeRepairResult(result AgentProviderResult) string {
	success := 0
	for _, item := range result.Results {
		if item.Status == "success" || item.Status == "restored" {
			success++
		}
	}
	if result.BackupID != "" {
		return fmt.Sprintf("%d/%d repairs completed; backup %s", success, len(result.Results), result.BackupID)
	}
	return fmt.Sprintf("%d/%d repairs completed", success, len(result.Results))
}

func summarizeAgentAppScan(scan AgentAppScanResult) string {
	if len(scan.Items) == 0 {
		return "Read-only local scan found no known agent apps"
	}
	return fmt.Sprintf("Read-only local scan found %d known agent app/config item(s)", len(scan.Items))
}

func renderAgentAppScanAnswer(scan AgentAppScanResult) string {
	var builder strings.Builder
	builder.WriteString("只读本机扫描已完成")
	if scan.OS != "" {
		builder.WriteString("（")
		builder.WriteString(scan.OS)
		builder.WriteString("）")
	}
	builder.WriteString("。\n")
	if len(scan.Items) == 0 {
		builder.WriteString("没有在常见应用目录和 Agent 配置目录里发现已知 Agent/AI 工具。")
		return builder.String()
	}
	builder.WriteString("发现这些可能的 Agent/AI 工具：\n")
	for _, item := range scan.Items {
		builder.WriteString("- ")
		builder.WriteString(item.Name)
		builder.WriteString("：")
		builder.WriteString(item.Kind)
		if item.Path != "" {
			builder.WriteString("，")
			builder.WriteString(item.Path)
		}
		builder.WriteString("\n")
	}
	builder.WriteString("本次只读取目录名称和路径，没有读取密钥或修改任何文件。")
	return strings.TrimSpace(builder.String())
}

func renderAgentConfigHealthAnswer(status AgentProviderInspectStatus) string {
	var builder strings.Builder
	healthy := 0
	for _, target := range status.Targets {
		if target.Healthy {
			healthy++
		}
	}
	builder.WriteString("只读健康检查已完成。")
	if status.TargetURL != "" {
		builder.WriteString("\nAINexus 目标地址：")
		builder.WriteString(status.TargetURL)
	}
	builder.WriteString(fmt.Sprintf("\n总体：%d/%d 项健康。", healthy, len(status.Targets)))
	for _, target := range status.Targets {
		builder.WriteString("\n- ")
		builder.WriteString(target.Label)
		if target.Label == "" {
			builder.WriteString(target.Target)
		}
		builder.WriteString("：")
		if target.Healthy {
			builder.WriteString("健康")
		} else if target.Status == "missing" {
			builder.WriteString("缺失")
		} else {
			builder.WriteString("需要处理")
		}
		if target.Path != "" {
			builder.WriteString("，")
			builder.WriteString(target.Path)
		}
		if len(target.Problems) > 0 {
			builder.WriteString("，")
			builder.WriteString(strings.Join(target.Problems, "；"))
		}
	}
	builder.WriteString("\n本次只读取配置状态，没有修复、写入或重启任何程序。")
	return strings.TrimSpace(builder.String())
}
