# AINexus AI Agent Design

## Goal

Add a built-in lightweight AI agent to AINexus that can use the currently configured AINexus endpoints to reason about user tasks, verify endpoint usability, and check or repair local agent configurations for Codex, OpenClaw, and Hermes on macOS and Windows.

## Context

AINexus is currently a desktop and server API endpoint rotation proxy. It already has:

- Endpoint failover and runtime status tracking in `internal/proxy`.
- Endpoint testing and model discovery in `internal/service/endpoint.go`.
- Wails desktop bindings in `cmd/desktop/app.go`.
- A native JavaScript desktop frontend in `cmd/desktop/frontend/src`.
- `AgentProviderService` in `internal/service/agent_provider.go`, which can detect, back up, apply, and restore local provider configuration for Claude, Codex, Gemini, OpenCode, OpenClaw, and Hermes.

The first agent version should extend these capabilities instead of becoming a full local coding agent. It should feel immediately useful inside AINexus while keeping local side effects narrow and auditable.

## Recommended Approach

Build a Chat/Task agent with controlled tools.

The agent should provide a task input panel where the user can ask AINexus to think, diagnose, and fix supported agent configuration issues. The model call goes through the local AINexus proxy so the existing endpoint rotation, token pool selection, request transformation, and failover logic remain authoritative.

The agent has a small tool set:

- `check_endpoints`: inspect enabled endpoint state and optionally run a minimal real request through AINexus.
- `check_agent_configs`: inspect Codex, OpenClaw, and Hermes configuration files and report whether they point to the current AINexus local URL.
- `repair_agent_configs`: back up and update Codex, OpenClaw, and Hermes configs through `AgentProviderService`, then verify the result.

The first version does not provide arbitrary shell execution, project file editing, or free-form filesystem access. Those can be added later behind explicit approval and sandboxing.

## Product Surface

### Desktop UI

Add an "AI Agent" entry near the existing Agent Provider action. The panel should include:

- A task textarea.
- A run button and a cancel button.
- Endpoint status summary: local AINexus URL, current endpoint if available, and whether the last model call succeeded.
- A transcript area that shows user prompt, assistant answer, and tool events.
- Tool result cards for endpoint checks and agent config checks.
- Repair controls for Codex, OpenClaw, and Hermes, with backup ID visible after writes.

The UI should stay consistent with the current native JS and CSS structure. Do not introduce Vue or a separate frontend framework.

### Server/Web UI

Expose the same core service through HTTP endpoints in the server Web UI after the desktop service is stable:

- `POST /api/agent/run`
- `POST /api/agent/check-configs`
- `POST /api/agent/repair-configs`

This keeps desktop and server behavior aligned, but desktop integration is the first implementation target because the goal is `ainexus.app` open-and-use behavior.

## Backend Architecture

### `AgentService`

Create `internal/service/agent.go` with a focused service:

```go
type AgentService struct {
    config        *config.Config
    proxy         *proxy.Proxy
    storage       *storage.SQLiteStorage
    endpoint      *EndpointService
    agentProvider *AgentProviderService
    httpClient    *http.Client
}
```

Responsibilities:

- Build a local model request against AINexus itself.
- Run a small, deterministic tool loop.
- Normalize responses into JSON suitable for Wails and Web UI callers.
- Reuse `EndpointService` and `AgentProviderService` for side effects and verification.

### Request and Response Types

```go
type AgentRunRequest struct {
    Task          string   `json:"task"`
    Tools         []string `json:"tools,omitempty"`
    RepairTargets []string `json:"repairTargets,omitempty"`
    MaxToolRounds int      `json:"maxToolRounds,omitempty"`
}

type AgentRunResult struct {
    Success       bool                `json:"success"`
    Answer        string              `json:"answer,omitempty"`
    EndpointURL   string              `json:"endpointUrl"`
    CurrentEndpoint string            `json:"currentEndpoint,omitempty"`
    Events        []AgentEvent        `json:"events"`
    ToolResults   []AgentToolResult   `json:"toolResults"`
    Error         string              `json:"error,omitempty"`
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
```

### Model Call Strategy

The service should call the local proxy URL, not upstream providers directly.

Preferred path:

- Use `http://127.0.0.1:<port>/v1/responses`.
- Send an OpenAI Responses-style request because Codex-compatible clients already use this route.
- Include instructions that the built-in agent has only the controlled tools listed above.

Fallback path:

- If `/v1/responses` fails due to client format incompatibility, retry once with `/v1/chat/completions`.
- The fallback should be visible in events.

The model call itself proves that at least one configured endpoint is currently usable. A successful call should record the returned answer and include current endpoint information from the proxy where available.

### Tool Loop

Use a small deterministic harness rather than provider-specific function calling in the first version.

Flow:

1. Run preflight checks before the model call:
   - Validate non-empty task.
   - Check that at least one endpoint is enabled.
   - Build the local proxy URL.
2. If the task contains clear repair intent or selected repair targets are present, run `check_agent_configs` first.
3. If repair is requested, run `repair_agent_configs`, then run `check_agent_configs` again.
4. Send the task, tool summaries, and endpoint state to the model.
5. Return the model answer plus structured tool results.

This gives reliable behavior without depending on every upstream provider supporting function calling identically.

## Agent Config Inspection

Extend `AgentProviderService` or add a companion inspector in `internal/service/agent_provider_inspector.go`.

The inspector should support Codex, OpenClaw, and Hermes in the first version:

- Codex:
  - Detect `~/.codex/config.toml`.
  - Check `model_provider = "AINexus"` or equivalent provider entry.
  - Check `[model_providers.AINexus].base_url == http://127.0.0.1:<port>/v1`.
  - Check `wire_api = "responses"`.
  - Detect `~/.codex/auth.json` and whether `OPENAI_API_KEY` is present. It may be the placeholder key.
- OpenClaw:
  - Detect `~/.openclaw/openclaw.json`.
  - Check `models.providers.AINexus.baseUrl == http://127.0.0.1:<port>/v1`.
  - Check `models.providers.AINexus.apiKey` is present.
  - Check default primary model references the AINexus provider if present.
- Hermes:
  - Detect `~/.hermes/config.yaml`.
  - Check provider name is `AINexus`.
  - Check base URL points to `http://127.0.0.1:<port>`.
  - Check API key placeholder is present where Hermes expects it.

Each target result should include:

- Target ID and label.
- Path.
- Detected/missing.
- Healthy/unhealthy.
- Problems.
- Suggested fix.

Missing configs are not an error unless the user asked to create missing configs. The repair path should support `CreateMissing`.

## Repair Behavior

Repair must reuse `AgentProviderService.Apply` for writes because it already handles:

- Target selection.
- Existing file backup.
- Atomic writes.
- Restore manifest creation.
- Platform-specific paths.

After repair:

1. Return the backup ID.
2. Re-run config inspection.
3. Run a minimal local proxy model call if endpoint verification is requested.

No repair should modify files outside the known Codex, OpenClaw, and Hermes configuration locations in the first version.

## Error Handling

Return structured errors with user-facing summaries:

- `no_task`: task is empty.
- `no_enabled_endpoints`: AINexus has no enabled endpoints.
- `proxy_unavailable`: local proxy is not listening or returns non-JSON failure.
- `model_call_failed`: proxy was reached but all endpoints failed.
- `config_parse_failed`: a supported config exists but cannot be parsed.
- `repair_failed`: backup or write failed.
- `unsupported_target`: unknown target requested.

Errors should also be appended to agent events so the UI can show progress rather than only a final failure.

## Security and Trust Boundary

The first version is intentionally constrained:

- No arbitrary shell commands.
- No project file edits.
- No scanning the user's home directory beyond known config files.
- No automatic repair unless the user clicks repair or sends a request with explicit repair intent.
- Every repair creates a backup when a target file already exists.
- The UI shows paths and backup IDs after repair.

This makes the feature safe enough for open-and-use desktop behavior on macOS and Windows.

## Cross-Platform Requirements

The backend should use Go standard library path handling and existing `AgentProviderService` path helpers.

Supported first-version targets:

- macOS:
  - `~/.codex/config.toml`
  - `~/.codex/auth.json`
  - `~/.openclaw/openclaw.json`
  - `~/.hermes/config.yaml`
- Windows:
  - `%USERPROFILE%\.codex\config.toml`
  - `%USERPROFILE%\.codex\auth.json`
  - `%USERPROFILE%\.openclaw\openclaw.json`
  - `%USERPROFILE%\.hermes\config.yaml`

The UI must not require users to install a separate runtime. The service ships inside the Wails app.

## Testing Strategy

### Unit Tests

Add tests for:

- Agent request validation.
- Local proxy URL construction.
- Responses request payload construction.
- Fallback to chat completions when responses call fails.
- Tool event ordering.
- Codex config inspection for healthy, missing, and broken states.
- OpenClaw config inspection for healthy, missing, and broken states.
- Hermes config inspection for healthy, missing, and broken states.
- Repair flow reuses `AgentProviderService` and returns backup ID.

### Integration Tests

Use `httptest.Server` to simulate the local proxy:

- Success response proves agent can return an answer.
- 500/503 response proves error reporting.
- Responses failure followed by chat completion success proves fallback.

Use temporary home directories for agent config inspection and repair tests.

### Frontend Checks

Add focused tests where the existing frontend test style supports it:

- Rendering tool results.
- Showing backup ID after repair.
- Disabling run button for empty task.
- Preserving transcript when a config check refreshes.

### Manual Verification

Before completion, verify:

- `go test ./... -count=1`
- Existing desktop frontend tests.
- Wails desktop starts on macOS.
- The AI Agent panel can run a task through a configured endpoint.
- Codex/OpenClaw/Hermes repair shows backup ID and post-repair healthy state.

Windows behavior should be covered by path unit tests and CI build checks when available.

## Non-Goals for First Version

- Full coding agent behavior.
- Shell command execution.
- Arbitrary file read/write.
- Long-running autonomous background tasks.
- Multi-agent orchestration.
- Provider-specific native tool calling.
- Automatic installation of Codex, OpenClaw, or Hermes.

These can be added later after the controlled agent harness is reliable.

## Acceptance Criteria

- AINexus desktop has a usable AI Agent panel.
- The built-in agent can answer a user task by calling the local AINexus proxy.
- Successful agent runs prove endpoint usability with a real model call.
- The agent can inspect Codex, OpenClaw, and Hermes configuration health.
- The agent can repair Codex, OpenClaw, and Hermes configurations through backup-protected writes.
- macOS and Windows config paths are handled by tests.
- No first-version code path runs arbitrary shell commands or edits arbitrary project files.
- Existing endpoint proxy, token pool, and failover behavior remains the source of truth.
