package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
)

func TestAgentRunRejectsEmptyTask(t *testing.T) {
	svc := NewAgentServiceWithOptions(config.DefaultConfig(), nil, nil, AgentServiceOptions{})

	result := svc.Run(AgentRunRequest{Task: "   "})

	if result.Success || result.Error != "no_task" {
		t.Fatalf("expected no_task failure, got %#v", result)
	}
}

func TestAgentRunRejectsNoEnabledEndpoints(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{Name: "disabled", Enabled: false}})
	svc := NewAgentServiceWithOptions(cfg, nil, nil, AgentServiceOptions{})

	result := svc.Run(AgentRunRequest{Task: "hello"})

	if result.Success || result.Error != "no_enabled_endpoints" {
		t.Fatalf("expected no_enabled_endpoints, got %#v", result)
	}
}

func TestAgentRunRepairsConfigsWithoutEnabledEndpoints(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3456)
	cfg.UpdateEndpoints([]config.Endpoint{{Name: "disabled", Enabled: false}})
	svc := NewAgentServiceWithOptions(cfg, nil, nil, AgentServiceOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	result := svc.Run(AgentRunRequest{
		Task:          "repair codex config",
		RepairTargets: []string{"codex"},
	})

	if result.Success || result.Error != "no_enabled_endpoints" {
		t.Fatalf("expected no_enabled_endpoints after local repair, got %#v", result)
	}
	if !hasTool(result.ToolResults, "check_agent_configs") || !hasTool(result.ToolResults, "repair_agent_configs") {
		t.Fatalf("expected local check and repair tools, got %#v", result.ToolResults)
	}
	if !strings.Contains(readFile(t, filepath.Join(home, ".codex", "config.toml")), "AINexus") {
		t.Fatalf("codex config was not repaired without enabled endpoints")
	}
}

func TestAgentRunChecksConfigsForCheckOnlyIntentWithoutRepairing(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3456)
	cfg.UpdateEndpoints([]config.Endpoint{{Name: "disabled", Enabled: false}})
	svc := NewAgentServiceWithOptions(cfg, nil, nil, AgentServiceOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	result := svc.Run(AgentRunRequest{Task: "检查配置状态"})

	if result.Error != "" || !result.Success {
		t.Fatalf("expected successful local config check, got %#v", result)
	}
	if !hasTool(result.ToolResults, "check_agent_configs") {
		t.Fatalf("expected check tool for check-only wording, got %#v", result.ToolResults)
	}
	if !strings.Contains(result.Answer, "只读健康检查已完成") {
		t.Fatalf("expected read-only health answer, got %q", result.Answer)
	}
	if hasTool(result.ToolResults, "repair_agent_configs") {
		t.Fatalf("did not expect repair tool for check-only wording, got %#v", result.ToolResults)
	}
	if fileExists(filepath.Join(home, ".codex", "config.toml")) {
		t.Fatalf("codex config should not be created for check-only wording")
	}
}

func TestAgentRunChecksNamedAgentForBeginnerWordingWithoutRepairing(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3456)
	cfg.UpdateEndpoints([]config.Endpoint{{Name: "disabled", Enabled: false}})
	svc := NewAgentServiceWithOptions(cfg, nil, nil, AgentServiceOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	result := svc.Run(AgentRunRequest{Task: "帮我检查 Codex 是否能用"})

	if result.Error != "" || !result.Success {
		t.Fatalf("expected successful local named-agent check, got %#v", result)
	}
	if !hasTool(result.ToolResults, "check_agent_configs") {
		t.Fatalf("expected check tool for named-agent wording, got %#v", result.ToolResults)
	}
	if !strings.Contains(result.Answer, "Codex") || !strings.Contains(result.Answer, "只读") {
		t.Fatalf("expected named-agent read-only answer, got %q", result.Answer)
	}
	if hasTool(result.ToolResults, "repair_agent_configs") {
		t.Fatalf("did not expect repair tool for named-agent wording, got %#v", result.ToolResults)
	}
	if fileExists(filepath.Join(home, ".codex", "config.toml")) {
		t.Fatalf("codex config should not be created for named-agent check wording")
	}
}

func TestAgentRunChecksOpenClawHealthForReadOnlyScanWording(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3456)
	cfg.UpdateEndpoints([]config.Endpoint{{Name: "disabled", Enabled: false}})
	svc := NewAgentServiceWithOptions(cfg, nil, nil, AgentServiceOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	result := svc.Run(AgentRunRequest{Task: "只读扫描下open claw的健康"})

	if result.Error != "" || !result.Success {
		t.Fatalf("expected successful local health check without endpoints, got %#v", result)
	}
	if !hasTool(result.ToolResults, "check_agent_configs") {
		t.Fatalf("expected check_agent_configs tool for open claw health wording, got %#v", result.ToolResults)
	}
	if hasTool(result.ToolResults, "repair_agent_configs") {
		t.Fatalf("did not expect repair tool for read-only health wording, got %#v", result.ToolResults)
	}
	assertInspectTargets(t, result.ToolResults, []string{"openclaw"})
	if !strings.Contains(result.Answer, "OpenClaw") || !strings.Contains(result.Answer, "只读") {
		t.Fatalf("expected local health answer to mention OpenClaw and read-only mode, got %q", result.Answer)
	}
	if fileExists(filepath.Join(home, ".openclaw", "config.json")) {
		t.Fatalf("openclaw config should not be created for read-only health wording")
	}
}

func TestAgentRunChecksClaudeHealthForNaturalWording(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3456)
	cfg.UpdateEndpoints([]config.Endpoint{{Name: "disabled", Enabled: false}})
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{
		"env": {
			"ANTHROPIC_BASE_URL": "http://127.0.0.1:3456",
			"ANTHROPIC_API_KEY": "ainexus-local"
		}
	}`)
	svc := NewAgentServiceWithOptions(cfg, nil, nil, AgentServiceOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	result := svc.Run(AgentRunRequest{Task: "检查下claude如何"})

	if result.Error != "" || !result.Success {
		t.Fatalf("expected successful local claude health check, got %#v", result)
	}
	if !hasTool(result.ToolResults, "check_agent_configs") {
		t.Fatalf("expected check_agent_configs tool for claude health wording, got %#v", result.ToolResults)
	}
	if hasTool(result.ToolResults, "repair_agent_configs") {
		t.Fatalf("did not expect repair tool for read-only claude wording, got %#v", result.ToolResults)
	}
	assertInspectTargets(t, result.ToolResults, []string{"claude"})
	if !strings.Contains(result.Answer, "Claude") || !strings.Contains(result.Answer, "健康") {
		t.Fatalf("expected local health answer to mention healthy Claude, got %q", result.Answer)
	}
}

func TestAgentRunScansLocalAgentAppsReadOnlyWithoutEnabledEndpoints(t *testing.T) {
	home := t.TempDir()
	appDir := filepath.Join(home, "Applications")
	mustMkdir(t, filepath.Join(appDir, "Cursor.app"))
	mustMkdir(t, filepath.Join(home, ".codex"))
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), "[profiles]\n")
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{Name: "disabled", Enabled: false}})
	svc := NewAgentServiceWithOptions(cfg, nil, nil, AgentServiceOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	result := svc.Run(AgentRunRequest{Task: "只读扫描下电脑有什么agent app"})

	if result.Error != "" || !result.Success {
		t.Fatalf("expected successful local scan without enabled endpoints, got %#v", result)
	}
	if !hasTool(result.ToolResults, "scan_agent_apps") {
		t.Fatalf("expected scan_agent_apps tool, got %#v", result.ToolResults)
	}
	if !strings.Contains(result.Answer, "Cursor") || !strings.Contains(result.Answer, "Codex") {
		t.Fatalf("expected scan answer to mention Cursor and Codex, got %q", result.Answer)
	}
	if !strings.Contains(result.Answer, "只读") && !strings.Contains(result.Answer, "read-only") {
		t.Fatalf("expected answer to explain read-only scan, got %q", result.Answer)
	}
}

func TestAgentRunIncludesLocalAgentAppScanInModelPrompt(t *testing.T) {
	home := t.TempDir()
	mustMkdir(t, filepath.Join(home, "Applications", "Cursor.app"))
	var payloadText string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		payloadText = string(mustJSON(t, payload))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":[{"content":[{"type":"output_text","text":"scan summarized"}]}]}`))
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{Name: "primary", Enabled: true}})
	svc := NewAgentServiceWithOptions(cfg, nil, nil, AgentServiceOptions{
		LocalBaseURL: server.URL,
		HTTPClient:   server.Client(),
		HomeDir:      home,
		DataDir:      filepath.Join(home, ".AINexus"),
	})

	result := svc.Run(AgentRunRequest{Task: "只读扫描下电脑有什么agent app"})

	if !result.Success || result.Answer != "scan summarized" {
		t.Fatalf("unexpected result %#v", result)
	}
	if !hasTool(result.ToolResults, "scan_agent_apps") {
		t.Fatalf("expected scan_agent_apps tool, got %#v", result.ToolResults)
	}
	if !strings.Contains(payloadText, "scan_agent_apps") || !strings.Contains(payloadText, "Cursor") {
		t.Fatalf("expected model prompt to include scan results, got %s", payloadText)
	}
}

func TestAgentRunUsesResponsesThenReturnsAnswer(t *testing.T) {
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if !strings.Contains(string(mustJSON(t, payload)), "Summarize endpoint status") {
			t.Fatalf("payload did not include task: %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":[{"content":[{"type":"output_text","text":"agent answer"}]}]}`))
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.UpdatePort(3000)
	cfg.UpdateEndpoints([]config.Endpoint{{Name: "primary", Enabled: true}})
	svc := NewAgentServiceWithOptions(cfg, nil, nil, AgentServiceOptions{
		LocalBaseURL: server.URL,
		HTTPClient:   server.Client(),
	})

	result := svc.Run(AgentRunRequest{Task: "Summarize endpoint status"})

	if !result.Success || result.Answer != "agent answer" || requestedPath != "/v1/responses" {
		t.Fatalf("unexpected result %#v path=%s", result, requestedPath)
	}
	if result.CurrentEndpoint != "primary" {
		t.Fatalf("expected current endpoint primary, got %q", result.CurrentEndpoint)
	}
}

func TestAgentRunFallsBackToChatCompletions(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/v1/responses" {
			http.Error(w, "unsupported", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"chat answer"}}]}`))
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{Name: "fallback", Enabled: true}})
	svc := NewAgentServiceWithOptions(cfg, nil, nil, AgentServiceOptions{
		LocalBaseURL: server.URL,
		HTTPClient:   server.Client(),
	})

	result := svc.Run(AgentRunRequest{Task: "hello"})

	if !result.Success || result.Answer != "chat answer" {
		t.Fatalf("unexpected result %#v", result)
	}
	if len(paths) != 2 || paths[0] != "/v1/responses" || paths[1] != "/v1/chat/completions" {
		t.Fatalf("expected responses then chat paths, got %#v", paths)
	}
}

func TestAgentRunChecksConfigsBeforeRepairAndAfterRepair(t *testing.T) {
	home := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":[{"content":[{"type":"output_text","text":"repair complete"}]}]}`))
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.UpdatePort(3456)
	cfg.UpdateEndpoints([]config.Endpoint{{Name: "primary", Enabled: true}})
	svc := NewAgentServiceWithOptions(cfg, nil, nil, AgentServiceOptions{
		LocalBaseURL: server.URL,
		HTTPClient:   server.Client(),
		HomeDir:      home,
		DataDir:      filepath.Join(home, ".AINexus"),
	})

	result := svc.Run(AgentRunRequest{
		Task:          "repair codex openclaw hermes configs",
		RepairTargets: []string{"codex", "openclaw", "hermes"},
	})

	if !result.Success || result.Answer != "repair complete" {
		t.Fatalf("unexpected result %#v", result)
	}
	if !hasTool(result.ToolResults, "check_agent_configs") || !hasTool(result.ToolResults, "repair_agent_configs") {
		t.Fatalf("expected check and repair tool results, got %#v", result.ToolResults)
	}
	if !strings.Contains(readFile(t, filepath.Join(home, ".codex", "config.toml")), "AINexus") {
		t.Fatalf("codex config was not repaired")
	}
}

func TestAgentCheckAndRepairJSONMethods(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3456)
	svc := NewAgentServiceWithOptions(cfg, nil, nil, AgentServiceOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	checkRaw := svc.CheckAgentConfigsJSON(`{"targets":["codex"]}`)
	if !strings.Contains(checkRaw, `"target":"codex"`) {
		t.Fatalf("check json missing codex: %s", checkRaw)
	}

	repairRaw := svc.RepairAgentConfigsJSON(`{"targets":["codex"],"createMissing":true}`)
	if !strings.Contains(repairRaw, `"target":"codex"`) || !strings.Contains(repairRaw, `"success"`) {
		t.Fatalf("repair json unexpected: %s", repairRaw)
	}
}

func hasTool(results []AgentToolResult, tool string) bool {
	for _, result := range results {
		if result.Tool == tool {
			return true
		}
	}
	return false
}

func assertInspectTargets(t *testing.T, results []AgentToolResult, want []string) {
	t.Helper()
	for _, result := range results {
		if result.Tool != "check_agent_configs" {
			continue
		}
		raw, err := json.Marshal(result.Data)
		if err != nil {
			t.Fatalf("marshal inspect data: %v", err)
		}
		var status AgentProviderInspectStatus
		if err := json.Unmarshal(raw, &status); err != nil {
			t.Fatalf("unmarshal inspect data: %v data=%s", err, raw)
		}
		got := make([]string, 0, len(status.Targets))
		for _, target := range status.Targets {
			got = append(got, target.Target)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("inspect targets=%v want %v", got, want)
		}
		return
	}
	t.Fatalf("check_agent_configs result not found in %#v", results)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
