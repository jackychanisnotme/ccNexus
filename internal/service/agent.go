package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	if !s.hasEnabledEndpoints() {
		result.Error = "no_enabled_endpoints"
		result.Events = append(result.Events, newAgentEvent("error", "No enabled endpoints are configured"))
		return result
	}
	if s.shouldRepair(req) {
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

func (s *AgentService) callResponses(task string, toolResults []AgentToolResult) (string, error) {
	payload := map[string]any{
		"model": "gpt-5",
		"input": []map[string]any{
			{
				"role": "system",
				"content": []map[string]any{{
					"type": "input_text",
					"text": agentSystemPrompt(),
				}},
			},
			{
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
	return "You are the built-in AINexus agent. Answer the user's task using the supplied endpoint and tool context. You cannot run shell commands or edit project files. You may summarize controlled AINexus tool results."
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
	for _, marker := range []string{"repair", "fix", "修复", "配置"} {
		if strings.Contains(task, marker) {
			return true
		}
	}
	return false
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
