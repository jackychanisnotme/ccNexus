# AINexus AI Agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a first-version built-in AINexus AI agent that can run user tasks through the local AINexus proxy, verify endpoint usability, and inspect or repair Codex/OpenClaw/Hermes configs with backup-protected writes.

**Architecture:** Add focused backend services under `internal/service`: an agent config inspector, a local proxy model client, and an `AgentService` orchestration layer. Bind the service through Wails and server Web UI APIs, then add a native JS desktop panel that renders transcript, tool events, config health, and repair results.

**Tech Stack:** Go 1.24+, Wails v2 bindings, existing AINexus proxy/config/storage services, `gopkg.in/yaml.v3`, native frontend ES modules, Vite build.

---

## File Map

- Create `internal/service/agent_provider_inspector.go`: read-only health inspection for Codex, OpenClaw, and Hermes config files.
- Create `internal/service/agent_provider_inspector_test.go`: temp-home tests for healthy, missing, broken, and platform-style path behavior.
- Create `internal/service/agent.go`: request/result types, deterministic tool loop, model-call orchestration, repair flow.
- Create `internal/service/agent_test.go`: service validation, tool event order, local proxy success/failure/fallback tests.
- Modify `internal/service/agent_provider.go`: expose target URL and target metadata helpers only if needed by the inspector; keep existing apply/restore behavior unchanged.
- Modify `cmd/desktop/app.go`: add `agent *service.AgentService`, initialize it, and expose Wails methods.
- Modify `cmd/desktop/frontend/src/modules/agent.js`: new desktop UI module for the AI Agent panel.
- Modify `cmd/desktop/frontend/src/main.js`: import/export AI Agent UI functions.
- Modify `cmd/desktop/frontend/src/modules/ui.js`: add AI Agent button near Agent Provider.
- Modify `cmd/desktop/frontend/src/i18n/en.js` and `cmd/desktop/frontend/src/i18n/zh-CN.js`: add user-visible strings.
- Modify `cmd/desktop/frontend/src/style.css`: add panel styles using existing modal and button conventions.
- Create `cmd/server/webui/api/agent.go`: HTTP endpoints for agent run/check/repair.
- Modify `cmd/server/webui/api/handler.go`: route agent endpoints.
- Create `cmd/server/webui/api/agent_test.go`: API routing tests with fake service dependencies where possible.
- Optionally modify `cmd/server/webui/ui`: defer full server UI if desktop UI is complete; API support is enough for this implementation pass.

---

### Task 1: Agent Config Inspector

**Files:**
- Create: `internal/service/agent_provider_inspector.go`
- Create: `internal/service/agent_provider_inspector_test.go`
- Modify: `internal/service/agent_provider.go` only if an exported `TargetURL()` helper is needed

- [ ] **Step 1: Write failing inspector tests**

Add `internal/service/agent_provider_inspector_test.go`:

```go
package service

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
)

func TestAgentProviderInspectorReportsHealthyCodexOpenClawAndHermes(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3456)
	provider := NewAgentProviderServiceWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})
	apply := provider.Apply(AgentProviderRequest{
		Targets:       []string{"codex", "openclaw", "hermes"},
		CreateMissing: true,
	})
	assertTargetStatus(t, apply.Results, "codex", "success")
	assertTargetStatus(t, apply.Results, "openclaw", "success")
	assertTargetStatus(t, apply.Results, "hermes", "success")

	inspector := NewAgentProviderInspectorWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})
	status := inspector.Inspect(AgentProviderInspectRequest{Targets: []string{"codex", "openclaw", "hermes"}})

	assertInspectHealthy(t, status.Targets, "codex")
	assertInspectHealthy(t, status.Targets, "openclaw")
	assertInspectHealthy(t, status.Targets, "hermes")
	if status.TargetURL != "http://127.0.0.1:3456" {
		t.Fatalf("TargetURL=%q", status.TargetURL)
	}
}

func TestAgentProviderInspectorReportsMissingAndBrokenConfigs(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(4567)
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), `model_provider = "Other"`)
	writeFile(t, filepath.Join(home, ".openclaw", "openclaw.json"), `{not json`)
	writeFile(t, filepath.Join(home, ".hermes", "config.yaml"), `model: [`)

	inspector := NewAgentProviderInspectorWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})
	status := inspector.Inspect(AgentProviderInspectRequest{Targets: []string{"codex", "openclaw", "hermes"}})

	codex := inspectTarget(t, status.Targets, "codex")
	if codex.Healthy || len(codex.Problems) == 0 {
		t.Fatalf("expected unhealthy codex with problems, got %#v", codex)
	}
	openclaw := inspectTarget(t, status.Targets, "openclaw")
	if openclaw.Status != "parse_failed" || !strings.Contains(strings.Join(openclaw.Problems, "\n"), "parse") {
		t.Fatalf("expected parse_failed openclaw, got %#v", openclaw)
	}
	hermes := inspectTarget(t, status.Targets, "hermes")
	if hermes.Status != "parse_failed" {
		t.Fatalf("expected parse_failed hermes, got %#v", hermes)
	}
}

func TestAgentProviderInspectorMissingConfigsAreNotHealthy(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	inspector := NewAgentProviderInspectorWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	status := inspector.Inspect(AgentProviderInspectRequest{Targets: []string{"codex", "openclaw", "hermes"}})
	for _, target := range []string{"codex", "openclaw", "hermes"} {
		item := inspectTarget(t, status.Targets, target)
		if item.Detected || item.Healthy || item.Status != "missing" {
			t.Fatalf("%s expected missing unhealthy, got %#v", target, item)
		}
	}
}

func assertInspectHealthy(t *testing.T, results []AgentProviderInspectTarget, target string) {
	t.Helper()
	item := inspectTarget(t, results, target)
	if !item.Detected || !item.Healthy || item.Status != "healthy" || len(item.Problems) != 0 {
		t.Fatalf("%s expected healthy, got %#v", target, item)
	}
}

func inspectTarget(t *testing.T, results []AgentProviderInspectTarget, target string) AgentProviderInspectTarget {
	t.Helper()
	for _, result := range results {
		if result.Target == target {
			return result
		}
	}
	t.Fatalf("target %s not found in %#v", target, results)
	return AgentProviderInspectTarget{}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internal/service -run 'TestAgentProviderInspector' -count=1
```

Expected: FAIL because `NewAgentProviderInspectorWithOptions`, `AgentProviderInspectRequest`, and `AgentProviderInspectTarget` are undefined.

- [ ] **Step 3: Implement inspector types and constructor**

Create `internal/service/agent_provider_inspector.go` with these public types and constructors:

```go
package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lich0821/ccNexus/internal/config"
	"gopkg.in/yaml.v3"
)

type AgentProviderInspectRequest struct {
	Targets []string `json:"targets,omitempty"`
}

type AgentProviderInspectStatus struct {
	TargetURL string                       `json:"targetUrl"`
	Targets   []AgentProviderInspectTarget `json:"targets"`
}

type AgentProviderInspectTarget struct {
	Target       string   `json:"target"`
	Label        string   `json:"label"`
	Path         string   `json:"path"`
	Detected     bool     `json:"detected"`
	Healthy      bool     `json:"healthy"`
	Status       string   `json:"status"`
	Problems     []string `json:"problems,omitempty"`
	SuggestedFix string   `json:"suggestedFix,omitempty"`
}

type AgentProviderInspector struct {
	provider *AgentProviderService
}

func NewAgentProviderInspector(cfg *config.Config) *AgentProviderInspector {
	return &AgentProviderInspector{provider: NewAgentProviderService(cfg)}
}

func NewAgentProviderInspectorWithOptions(cfg *config.Config, options AgentProviderOptions) *AgentProviderInspector {
	return &AgentProviderInspector{provider: NewAgentProviderServiceWithOptions(cfg, options)}
}
```

Then implement:

```go
func (i *AgentProviderInspector) Inspect(req AgentProviderInspectRequest) AgentProviderInspectStatus
func (i *AgentProviderInspector) InspectJSON(targetsJSON string) string
func (i *AgentProviderInspector) inspectCodex() AgentProviderInspectTarget
func (i *AgentProviderInspector) inspectOpenClaw() AgentProviderInspectTarget
func (i *AgentProviderInspector) inspectHermes() AgentProviderInspectTarget
```

Use `i.provider.targetURL()` for `TargetURL`. Because this is the same package, no exported helper is required.

- [ ] **Step 4: Implement target selection and missing status**

Inside `Inspect`, select only `codex`, `openclaw`, and `hermes`. Unknown targets should return an item with `Status: "unsupported"` and `SuggestedFix: "Select Codex, OpenClaw, or Hermes."`.

For missing configs, return:

```go
AgentProviderInspectTarget{
	Target: targetID,
	Label: label,
	Path: path,
	Detected: false,
	Healthy: false,
	Status: "missing",
	SuggestedFix: "Create or repair this config through AINexus.",
}
```

- [ ] **Step 5: Implement Codex inspection**

Read `~/.codex/config.toml` and `~/.codex/auth.json` using simple string checks because current code writes a small deterministic TOML file. Mark problems for missing:

- `model_provider = "AINexus"`
- `[model_providers.AINexus]`
- `base_url = "<targetURL>/v1"`
- `wire_api = "responses"`
- `OPENAI_API_KEY` in auth JSON

Parse `auth.json` with `encoding/json`; a malformed auth file should produce `Status: "parse_failed"`.

- [ ] **Step 6: Implement OpenClaw inspection**

Use existing `readLooseJSONFile(path)` from `agent_provider.go`. Check:

- `models.providers.AINexus` exists.
- `baseUrl == targetURL + "/v1"`.
- `apiKey` is non-empty.
- If `agents.defaults.model.primary` exists, it starts with `AINexus/`.

Malformed loose JSON should produce `Status: "parse_failed"` with a parse problem.

- [ ] **Step 7: Implement Hermes inspection**

Use `yaml.Unmarshal` into `map[string]any`. Check:

- `model.provider == "AINexus"`.
- `model.base_url == targetURL`.
- A `custom_providers` entry named `AINexus` exists.
- That provider has `base_url == targetURL + "/v1"` and a non-empty `api_key`.

Malformed YAML should produce `Status: "parse_failed"`.

- [ ] **Step 8: Run tests and verify GREEN**

Run:

```bash
go test ./internal/service -run 'TestAgentProviderInspector' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/service/agent_provider_inspector.go internal/service/agent_provider_inspector_test.go
git commit -m "feat: inspect agent provider configs"
```

---

### Task 2: Agent Service Model Client and Validation

**Files:**
- Create: `internal/service/agent.go`
- Create: `internal/service/agent_test.go`

- [ ] **Step 1: Write failing validation and model-call tests**

Create `internal/service/agent_test.go`:

```go
package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		HTTPClient:  server.Client(),
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
		HTTPClient:  server.Client(),
	})

	result := svc.Run(AgentRunRequest{Task: "hello"})

	if !result.Success || result.Answer != "chat answer" {
		t.Fatalf("unexpected result %#v", result)
	}
	if len(paths) != 2 || paths[0] != "/v1/responses" || paths[1] != "/v1/chat/completions" {
		t.Fatalf("expected responses then chat paths, got %#v", paths)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internal/service -run 'TestAgentRun' -count=1
```

Expected: FAIL because `AgentService`, `AgentRunRequest`, and related types are undefined.

- [ ] **Step 3: Implement public types and constructors**

Create `internal/service/agent.go`:

```go
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
```

Add the request/result/event/tool result structs from the design spec. Add:

```go
func NewAgentService(cfg *config.Config, p *proxy.Proxy, s *storage.SQLiteStorage, endpoint *EndpointService, provider *AgentProviderService) *AgentService
func NewAgentServiceWithOptions(cfg *config.Config, p *proxy.Proxy, s *storage.SQLiteStorage, options AgentServiceOptions) *AgentService
func (s *AgentService) Run(req AgentRunRequest) AgentRunResult
func (s *AgentService) RunJSON(requestJSON string) string
```

- [ ] **Step 4: Implement validation and base URL**

In `Run`:

- Trim task.
- If empty, return `Success: false`, `Error: "no_task"`.
- Count enabled endpoints from `s.config.GetEndpoints()`.
- If none, return `Error: "no_enabled_endpoints"`.
- Use `s.localBaseURL` if provided, else `fmt.Sprintf("http://127.0.0.1:%d", s.config.GetPort())`.

Append `AgentEvent{Type: "preflight", Message: "..."}` for validation progress.

- [ ] **Step 5: Implement Responses payload and answer parser**

Add helpers:

```go
func (s *AgentService) callResponses(task string, toolResults []AgentToolResult) (string, error)
func parseResponsesAnswer(data []byte) string
```

Send:

```json
{
  "model": "gpt-5",
  "input": [
    {"role":"system","content":[{"type":"input_text","text":"You are the built-in AINexus agent..."}]},
    {"role":"user","content":[{"type":"input_text","text":"<task and tool summary>"}]}
  ],
  "max_output_tokens": 800
}
```

Set `Authorization: Bearer ainexus-local` and `Content-Type: application/json`; the proxy ignores the placeholder key for token pool modes and uses endpoint config for upstream auth.

Parse all of these shapes:

- `output[].content[].text`
- `output_text`
- `choices[0].message.content` as defensive compatibility

- [ ] **Step 6: Implement chat fallback**

Add:

```go
func (s *AgentService) callChatCompletions(task string, toolResults []AgentToolResult) (string, error)
func parseChatAnswer(data []byte) string
```

Send:

```json
{
  "model": "gpt-5",
  "messages": [
    {"role":"system","content":"You are the built-in AINexus agent..."},
    {"role":"user","content":"<task and tool summary>"}
  ],
  "max_tokens": 800
}
```

Fallback only after Responses returns a non-2xx response or an empty answer. Add an event with `Type: "model_fallback"`.

- [ ] **Step 7: Run tests and verify GREEN**

Run:

```bash
go test ./internal/service -run 'TestAgentRun' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/service/agent.go internal/service/agent_test.go
git commit -m "feat: add built-in agent service"
```

---

### Task 3: Agent Tools for Config Check and Repair

**Files:**
- Modify: `internal/service/agent.go`
- Modify: `internal/service/agent_test.go`

- [ ] **Step 1: Write failing tool-flow tests**

Append to `internal/service/agent_test.go`:

```go
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
		HTTPClient:  server.Client(),
		HomeDir:     home,
		DataDir:     filepath.Join(home, ".AINexus"),
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
```

Also add imports `path/filepath` if not already present.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internal/service -run 'TestAgent.*Config|TestAgentRunChecks' -count=1
```

Expected: FAIL because JSON methods and tool loop repair behavior are not implemented.

- [ ] **Step 3: Add check and repair request types**

In `internal/service/agent.go`, add:

```go
type AgentConfigRepairRequest struct {
	Targets       []string `json:"targets,omitempty"`
	CreateMissing bool     `json:"createMissing,omitempty"`
}
```

Add methods:

```go
func (s *AgentService) CheckAgentConfigs(req AgentProviderInspectRequest) AgentProviderInspectStatus
func (s *AgentService) CheckAgentConfigsJSON(requestJSON string) string
func (s *AgentService) RepairAgentConfigs(req AgentConfigRepairRequest) AgentProviderResult
func (s *AgentService) RepairAgentConfigsJSON(requestJSON string) string
```

- [ ] **Step 4: Wire deterministic tool loop**

In `Run`:

- Determine repair intent with `len(req.RepairTargets) > 0` or task containing one of `repair`, `fix`, `修复`, `配置`.
- If repair intent, call inspector first and append an `AgentToolResult{Tool: "check_agent_configs"}`.
- Call `RepairAgentConfigs` with targets from `RepairTargets`, defaulting to `codex`, `openclaw`, `hermes` if task contains repair intent and no targets.
- Append `AgentToolResult{Tool: "repair_agent_configs"}` with backup ID in summary when present.
- Call inspector again and append a second `check_agent_configs` result.

Keep side effects limited to `AgentProviderService.Apply`.

- [ ] **Step 5: Include tool summaries in model prompt**

Update prompt construction to include compact JSON summaries of `ToolResults`. Keep raw file paths visible; do not include API keys.

- [ ] **Step 6: Run tests and verify GREEN**

Run:

```bash
go test ./internal/service -run 'TestAgent' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/service/agent.go internal/service/agent_test.go
git commit -m "feat: add agent config tools"
```

---

### Task 4: Desktop Wails Bindings

**Files:**
- Modify: `cmd/desktop/app.go`
- Create or modify: `cmd/desktop/app_agent_test.go`

- [ ] **Step 1: Write failing desktop binding test**

Create `cmd/desktop/app_agent_test.go`:

```go
package main

import (
	"encoding/json"
	"testing"
)

func TestAppAgentBindingsReturnJSONErrorsBeforeStartup(t *testing.T) {
	app := NewApp(nil)

	raw := app.RunAgent(`{"task":""}`)
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("invalid json: %v raw=%s", err, raw)
	}
	if result["success"] != false {
		t.Fatalf("expected failure before startup, got %#v", result)
	}
}
```

- [ ] **Step 2: Run test and verify RED**

Run:

```bash
go test ./cmd/desktop -run TestAppAgentBindingsReturnJSONErrorsBeforeStartup -count=1
```

Expected: FAIL because `RunAgent` is undefined.

- [ ] **Step 3: Add service field and startup initialization**

In `cmd/desktop/app.go`, add to `App`:

```go
agent *service.AgentService
```

After `a.agentProvider = service.NewAgentProviderService(a.config)`, initialize:

```go
a.agent = service.NewAgentService(a.config, a.proxy, a.storage, a.endpoint, a.agentProvider)
```

- [ ] **Step 4: Add Wails binding methods**

Add near AgentProvider bindings:

```go
func (a *App) ensureAgentService() *service.AgentService {
	if a.agent == nil && a.config != nil {
		if a.agentProvider == nil {
			a.agentProvider = service.NewAgentProviderService(a.config)
		}
		a.agent = service.NewAgentService(a.config, a.proxy, a.storage, a.endpoint, a.agentProvider)
	}
	return a.agent
}

func (a *App) RunAgent(requestJSON string) string {
	svc := a.ensureAgentService()
	if svc == nil {
		return desktopErrorJSON(fmt.Errorf("agent unavailable"))
	}
	return svc.RunJSON(requestJSON)
}

func (a *App) CheckAgentConfigs(requestJSON string) string {
	svc := a.ensureAgentService()
	if svc == nil {
		return desktopErrorJSON(fmt.Errorf("agent unavailable"))
	}
	return svc.CheckAgentConfigsJSON(requestJSON)
}

func (a *App) RepairAgentConfigs(requestJSON string) string {
	svc := a.ensureAgentService()
	if svc == nil {
		return desktopErrorJSON(fmt.Errorf("agent unavailable"))
	}
	return svc.RepairAgentConfigsJSON(requestJSON)
}
```

Use existing `desktopErrorJSON` and existing `fmt` import.

- [ ] **Step 5: Run test and verify GREEN**

Run:

```bash
go test ./cmd/desktop -run TestAppAgentBindingsReturnJSONErrorsBeforeStartup -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/desktop/app.go cmd/desktop/app_agent_test.go
git commit -m "feat: expose desktop agent bindings"
```

---

### Task 5: Server Web UI API Endpoints

**Files:**
- Create: `cmd/server/webui/api/agent.go`
- Create: `cmd/server/webui/api/agent_test.go`
- Modify: `cmd/server/webui/api/handler.go`

- [ ] **Step 1: Write failing API routing test**

Create `cmd/server/webui/api/agent_test.go`:

```go
package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/service"
)

func TestAgentAPIRejectsEmptyTask(t *testing.T) {
	cfg := config.DefaultConfig()
	handler := &Handler{
		config: cfg,
		agent: service.NewAgentServiceWithOptions(cfg, nil, nil, service.AgentServiceOptions{}),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agent/run", bytes.NewReader([]byte(`{"task":""}`)))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"no_task"`) {
		t.Fatalf("unexpected response code=%d body=%s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run test and verify RED**

Run:

```bash
go test ./cmd/server/webui/api -run TestAgentAPIRejectsEmptyTask -count=1
```

Expected: FAIL because `Handler.agent` and routes are undefined.

- [ ] **Step 3: Add handler field and constructor wiring**

In `cmd/server/webui/api/handler.go`, add:

```go
agent *service.AgentService
```

In `NewHandler`, after `agentProvider`, initialize:

```go
agent: service.NewAgentService(cfg, p, s, service.NewEndpointService(cfg, p, s), newAgentProviderService(cfg, s)),
```

If this duplicates provider creation, assign provider to a local variable before the struct literal.

- [ ] **Step 4: Add routes**

In `ServeHTTP`, add cases:

```go
case "/api/agent/run":
	authMiddleware(http.HandlerFunc(h.handleAgentRun)).ServeHTTP(w, r)
case "/api/agent/check-configs":
	authMiddleware(http.HandlerFunc(h.handleAgentCheckConfigs)).ServeHTTP(w, r)
case "/api/agent/repair-configs":
	authMiddleware(http.HandlerFunc(h.handleAgentRepairConfigs)).ServeHTTP(w, r)
```

- [ ] **Step 5: Implement API handlers**

Create `cmd/server/webui/api/agent.go`:

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/lich0821/ccNexus/internal/service"
)

func (h *Handler) handleAgentRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req service.AgentRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	WriteJSON(w, h.agent.Run(req))
}
```

Add analogous `handleAgentCheckConfigs` and `handleAgentRepairConfigs` methods.

- [ ] **Step 6: Run test and verify GREEN**

Run:

```bash
go test ./cmd/server/webui/api -run TestAgentAPIRejectsEmptyTask -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/server/webui/api/agent.go cmd/server/webui/api/agent_test.go cmd/server/webui/api/handler.go
git commit -m "feat: expose agent web api"
```

---

### Task 6: Desktop AI Agent UI

**Files:**
- Create: `cmd/desktop/frontend/src/modules/agent.js`
- Modify: `cmd/desktop/frontend/src/main.js`
- Modify: `cmd/desktop/frontend/src/modules/ui.js`
- Modify: `cmd/desktop/frontend/src/i18n/en.js`
- Modify: `cmd/desktop/frontend/src/i18n/zh-CN.js`
- Modify: `cmd/desktop/frontend/src/style.css`

- [ ] **Step 1: Add i18n strings**

In `cmd/desktop/frontend/src/i18n/en.js`, add:

```js
agent: {
    title: 'AI Agent',
    button: 'AI Agent',
    taskPlaceholder: 'Ask AINexus to diagnose endpoints, repair agent configs, or answer a task...',
    run: 'Run',
    cancel: 'Cancel',
    checkConfigs: 'Check configs',
    repairConfigs: 'Repair configs',
    endpoint: 'Endpoint',
    tools: 'Tools',
    transcript: 'Transcript',
    noTask: 'Enter a task first',
    runFailed: 'Agent run failed: {error}',
    repairComplete: 'Repair complete',
    backupId: 'Backup ID',
    healthy: 'Healthy',
    missing: 'Missing',
    unhealthy: 'Needs attention'
}
```

Add corresponding Chinese strings in `zh-CN.js`.

- [ ] **Step 2: Create agent UI module**

Create `cmd/desktop/frontend/src/modules/agent.js` with:

```js
import { t } from '../i18n/index.js';
import { showNotification } from './modal.js';

let lastResult = null;

function escapeHtml(value) {
    return String(value ?? '')
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#039;');
}
```

Implement exports:

```js
export function showAgentModal()
export function closeAgentModal()
export async function runAgent()
export async function checkAgentConfigs()
export async function repairAgentConfigs()
```

The modal should include:

- `textarea#agentTask`
- checkboxes for `codex`, `openclaw`, `hermes`
- `button` for check and repair
- `button#agentRunButton`
- `div#agentResults`

- [ ] **Step 3: Render results**

In `agent.js`, implement:

```js
function renderAgentResult(result)
function renderToolResult(tool)
function renderConfigTargets(targets)
```

Show:

- `result.answer`
- `result.endpointUrl`
- `result.currentEndpoint`
- event list
- tool result summaries
- target health rows
- backup ID if present in repair result

- [ ] **Step 4: Wire Wails calls**

Use:

```js
const raw = await window.go.main.App.RunAgent(JSON.stringify({
    task,
    repairTargets: selectedTargets
}));
```

For check:

```js
await window.go.main.App.CheckAgentConfigs(JSON.stringify({ targets: selectedTargets }));
```

For repair:

```js
await window.go.main.App.RepairAgentConfigs(JSON.stringify({
    targets: selectedTargets,
    createMissing: true
}));
```

Disable run button while request is in flight and restore it in `finally`.

- [ ] **Step 5: Wire main exports**

In `cmd/desktop/frontend/src/main.js`, import:

```js
import { showAgentModal, closeAgentModal, runAgent, checkAgentConfigs, repairAgentConfigs } from './modules/agent.js'
```

Expose:

```js
window.showAgentModal = showAgentModal;
window.closeAgentModal = closeAgentModal;
window.runAgent = runAgent;
window.checkAgentConfigs = checkAgentConfigs;
window.repairAgentConfigs = repairAgentConfigs;
```

- [ ] **Step 6: Add header button**

In `cmd/desktop/frontend/src/modules/ui.js`, add before Agent Provider:

```html
<button class="btn btn-secondary" onclick="window.showAgentModal()">
    ✨ ${t('agent.button')}
</button>
```

- [ ] **Step 7: Add CSS**

In `cmd/desktop/frontend/src/style.css`, add styles:

```css
.agent-modal {
    max-width: 860px;
    width: min(92vw, 860px);
}

.agent-task-input {
    width: 100%;
    min-height: 110px;
    resize: vertical;
}

.agent-targets,
.agent-actions,
.agent-summary,
.agent-tool-results,
.agent-events {
    display: flex;
    gap: 10px;
    flex-wrap: wrap;
}

.agent-result-card,
.agent-target-row,
.agent-event-row {
    border: 1px solid var(--border-color);
    border-radius: 8px;
    padding: 10px;
    background: var(--bg-secondary);
}

.agent-target-row.healthy {
    border-color: rgba(34, 197, 94, 0.4);
}

.agent-target-row.unhealthy,
.agent-target-row.parse_failed {
    border-color: rgba(239, 68, 68, 0.45);
}
```

Adjust variable names if the current CSS uses different tokens; keep the card radius at 8px or less.

- [ ] **Step 8: Build frontend**

Run:

```bash
cd cmd/desktop/frontend && npm run build
```

Expected: PASS, Vite build completes.

- [ ] **Step 9: Commit**

```bash
git add cmd/desktop/frontend/src/modules/agent.js cmd/desktop/frontend/src/main.js cmd/desktop/frontend/src/modules/ui.js cmd/desktop/frontend/src/i18n/en.js cmd/desktop/frontend/src/i18n/zh-CN.js cmd/desktop/frontend/src/style.css
git commit -m "feat: add desktop ai agent panel"
```

---

### Task 7: End-to-End Verification and Polish

**Files:**
- Modify as needed based on failures from prior tasks.
- Update docs only if behavior differs from the design.

- [ ] **Step 1: Run backend service tests**

Run:

```bash
go test ./internal/service -count=1
```

Expected: PASS.

- [ ] **Step 2: Run desktop binding tests**

Run:

```bash
go test ./cmd/desktop -count=1
```

Expected: PASS.

- [ ] **Step 3: Run server API tests**

Run:

```bash
go test ./cmd/server/webui/api -count=1
```

Expected: PASS.

- [ ] **Step 4: Run full Go test suite**

Run:

```bash
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 5: Run frontend build**

Run:

```bash
cd cmd/desktop/frontend && npm run build
```

Expected: PASS.

- [ ] **Step 6: Launch desktop dev app for manual smoke**

Run:

```bash
cd cmd/desktop && wails dev
```

Expected: app starts, proxy starts, and no startup panic appears in terminal logs.

Manual checks:

- AI Agent button opens the panel.
- Empty task is rejected in UI.
- Config check shows Codex/OpenClaw/Hermes rows.
- Repair creates or updates selected configs and displays backup ID.
- With at least one real configured endpoint, a task returns a model answer through AINexus.

- [ ] **Step 7: Commit final fixes**

If any verification changes were needed:

```bash
git status --short
git add path/from/status.go path/from/status.js
git commit -m "fix: polish ai agent integration"
```

Replace the two `git add` paths with the actual files shown by `git status --short`. If no changes were needed, do not create an empty commit.

---

## Self-Review Checklist

- Spec coverage:
  - Built-in task agent: Tasks 2, 4, 6, 7.
  - Uses AINexus endpoints through local proxy: Task 2.
  - Endpoint usability verification via real model call: Task 2 and Task 7.
  - Check/repair Codex/OpenClaw/Hermes: Tasks 1 and 3.
  - macOS/Windows path handling: Task 1 temp-home and existing path helpers; Task 7 full tests.
  - Open-and-use desktop integration: Tasks 4 and 6.
  - Light reference to Codex/OpenClaw/Hermes behavior: inspector checks and repair formats in Tasks 1 and 3.
  - No arbitrary shell or project file edits: Task 3 side-effect boundary and Task 7 manual check.
- Placeholder scan: no `TBD`, `TODO`, or template-only future steps should remain.
- Type consistency: use `AgentRunRequest`, `AgentRunResult`, `AgentEvent`, `AgentToolResult`, `AgentProviderInspectRequest`, `AgentProviderInspectStatus`, and `AgentProviderInspectTarget` consistently across backend, desktop, and API tasks.
