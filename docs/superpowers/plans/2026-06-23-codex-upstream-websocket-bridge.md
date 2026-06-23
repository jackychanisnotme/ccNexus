# Codex Upstream WebSocket Bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route streaming Codex Token Pool Responses requests through the official upstream WebSocket transport while preserving AINexus's existing downstream SSE contract and HTTP fallback.

**Architecture:** Add a focused WebSocket adapter that dials the official Codex Responses endpoint, sends a `response.create` frame, and exposes received JSON frames through an `io.Pipe` as a synthetic SSE `http.Response`. The existing streaming pipeline remains responsible for event forwarding, token accounting, completion validation, and downstream errors. The proxy attempts this adapter only for streaming `codex_token_pool` Responses requests and falls back to HTTP/SSE only for unsupported WebSocket handshakes.

**Tech Stack:** Go 1.24, `github.com/gorilla/websocket`, existing `internal/proxy` streaming and retry infrastructure.

---

### Task 1: WebSocket Protocol Helpers

**Files:**
- Create: `internal/proxy/codex_websocket.go`
- Create: `internal/proxy/codex_websocket_test.go`
- Modify: `go.mod`

- [ ] **Step 1: Write failing tests for URL and frame conversion**

Add table tests that require `codexWebSocketURL("https://chatgpt.com/backend-api/codex/responses")` to return `wss://chatgpt.com/backend-api/codex/responses`, reject non-HTTP schemes, and require `buildCodexWebSocketFrame` to preserve `model`, `input`, `tools`, and `prompt_cache_key` while adding `"type":"response.create"`.

```go
func TestBuildCodexWebSocketFrameAddsResponseCreateType(t *testing.T) {
    raw := []byte(`{"model":"gpt-5.5","input":[],"tools":[{"type":"custom","name":"exec"}],"prompt_cache_key":"trace-1"}`)
    frame, err := buildCodexWebSocketFrame(raw)
    if err != nil { t.Fatal(err) }
    var payload map[string]any
    if err := json.Unmarshal(frame, &payload); err != nil { t.Fatal(err) }
    if payload["type"] != "response.create" { t.Fatalf("type=%v", payload["type"]) }
    if payload["prompt_cache_key"] != "trace-1" { t.Fatalf("prompt_cache_key=%v", payload["prompt_cache_key"]) }
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run: `go test ./internal/proxy -run 'Test(BuildCodexWebSocketFrame|CodexWebSocketURL)' -count=1`

Expected: build failure because the helper functions do not exist.

- [ ] **Step 3: Implement minimal protocol helpers**

Create `codex_websocket.go` with:

```go
var errCodexWebSocketUnsupported = errors.New("codex responses websocket unsupported")

func codexWebSocketURL(rawURL string) (string, error) {
    parsed, err := url.Parse(rawURL)
    if err != nil { return "", err }
    switch parsed.Scheme {
    case "https": parsed.Scheme = "wss"
    case "http": parsed.Scheme = "ws"
    default: return "", fmt.Errorf("unsupported websocket source scheme %q", parsed.Scheme)
    }
    return parsed.String(), nil
}

func buildCodexWebSocketFrame(payload []byte) ([]byte, error) {
    var body map[string]any
    if err := json.Unmarshal(payload, &body); err != nil { return nil, err }
    body["type"] = "response.create"
    return json.Marshal(body)
}
```

Promote `github.com/gorilla/websocket v1.5.3` from indirect to direct in `go.mod`.

- [ ] **Step 4: Run the focused test and verify GREEN**

Run: `go test ./internal/proxy -run 'Test(BuildCodexWebSocketFrame|CodexWebSocketURL)' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit protocol helpers**

```bash
git add go.mod internal/proxy/codex_websocket.go internal/proxy/codex_websocket_test.go
git commit -m "feat(proxy): add Codex websocket protocol helpers"
```

### Task 2: WebSocket-To-SSE Adapter

**Files:**
- Modify: `internal/proxy/codex_websocket.go`
- Modify: `internal/proxy/codex_websocket_test.go`
- Modify: `internal/proxy/proxy.go`
- Modify: `internal/proxy/streaming.go`

- [ ] **Step 1: Write failing adapter tests with a local WebSocket server**

Add tests using `httptest.NewServer` plus `websocket.Upgrader` that verify:

```go
func TestOpenCodexWebSocketStreamBridgesCompletedResponseToSSE(t *testing.T) {
    // Server verifies Authorization and chatgpt-account-id, reads one
    // response.create frame, then sends response.created, a large
    // response.custom_tool_call_input.delta, and response.completed.
    // The returned synthetic response body must contain all three SSE data lines.
}

func TestOpenCodexWebSocketStreamReturnsReadErrorWhenClosedBeforeCompleted(t *testing.T) {
    // Server sends response.created and custom_tool_call delta, then closes.
    // Reading the synthetic body must return an error containing
    // "before response.completed".
}
```

Use a payload above 2 MB in the success test so the bridge proves it does not reintroduce the SSE scanner truncation failure.

- [ ] **Step 2: Run adapter tests and verify RED**

Run: `go test ./internal/proxy -run 'TestOpenCodexWebSocketStream' -count=1`

Expected: build failure because the adapter and injectable dialer do not exist.

- [ ] **Step 3: Implement the adapter and lifecycle**

Add an injectable dial function to `Proxy`:

```go
type codexWebSocketDialFunc func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)
```

Initialize it from a `websocket.Dialer` that honors the endpoint/global Codex proxy. Implement:

```go
func (p *Proxy) openCodexWebSocketStream(
    ctx context.Context,
    proxyReq *http.Request,
    endpoint config.Endpoint,
    payload []byte,
) (*http.Response, error)
```

The function must clone safe request headers, remove HTTP Upgrade-reserved headers, dial upstream, send the frame, and return an `http.Response` whose body is an `io.PipeReader`. A goroutine reads text frames, validates each JSON `type`, writes `data: <frame>\n\n`, and closes normally only after `response.completed`. Error events, binary frames, connection close, EOF, context cancellation, and idle timeout close the pipe with an error. The synthetic response copies safe handshake headers and sets `Content-Type: text/event-stream`.

Set the WebSocket read limit and both production SSE scanners to `128 * 1024 * 1024` bytes while retaining the current 128 KB initial allocation. This permits large custom-tool frames without reserving 128 MB per request up front.

- [ ] **Step 4: Run adapter tests and verify GREEN**

Run: `go test ./internal/proxy -run 'TestOpenCodexWebSocketStream' -count=1`

Expected: PASS with both completion and premature-close behavior verified.

- [ ] **Step 5: Commit the adapter**

```bash
git add internal/proxy/codex_websocket.go internal/proxy/codex_websocket_test.go internal/proxy/proxy.go internal/proxy/streaming.go
git commit -m "feat(proxy): bridge Codex websocket events to SSE"
```

### Task 3: Proxy Routing, Fallback, And Completion Safety

**Files:**
- Modify: `internal/proxy/proxy.go`
- Modify: `internal/proxy/codex_websocket_test.go`
- Modify: `internal/proxy/responses_stream_completion_test.go`

- [ ] **Step 1: Write failing routing and fallback tests**

Add tests proving:

```go
func TestCodexTokenPoolStreamingResponsesPreferWebSocket(t *testing.T) {}
func TestCodexWebSocketUnsupportedHandshakeFallsBackToHTTP(t *testing.T) {}
func TestNonCodexResponsesDoNotUseCodexWebSocket(t *testing.T) {}
func TestCodexPendingCustomToolMissingCompletionIsNeverTolerated(t *testing.T) {}
```

The unsupported-handshake test returns HTTP 426 from the injected dialer and expects one existing HTTP/SSE request. The pending-tool test uses a Codex User-Agent and expects a typed downstream error rather than tolerant success.

- [ ] **Step 2: Run routing tests and verify RED**

Run: `go test ./internal/proxy -run 'Test(CodexTokenPoolStreamingResponsesPreferWebSocket|CodexWebSocketUnsupportedHandshakeFallsBackToHTTP|NonCodexResponsesDoNotUseCodexWebSocket|CodexPendingCustomToolMissingCompletionIsNeverTolerated)' -count=1`

Expected: failures showing streaming Codex traffic still uses HTTP and pending custom tools are still tolerated.

- [ ] **Step 3: Integrate WebSocket selection and guarded fallback**

Before `sendRequestWithResponseHeaderTimeout`, call the adapter only when all conditions hold:

```go
endpoint.AuthMode == config.AuthModeCodexTokenPool &&
streamReq.Stream &&
clientFormat == ClientFormatOpenAIResponses &&
transformerName == "cx_resp_openai2"
```

Fall back to the existing HTTP request only when the adapter returns `errCodexWebSocketUnsupported` for status 404, 405, or 426 and no downstream bytes exist. Other dial errors continue through existing retry and failover classification. Update the missing-completion tolerant branch so `ResponsesCompletionSafe.ToolPending` always emits the typed error and records failure, even for Codex User-Agents.

- [ ] **Step 4: Run routing tests and the full proxy package**

Run: `go test ./internal/proxy -count=1`

Expected: PASS.

- [ ] **Step 5: Run repository verification**

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 6: Commit integration**

```bash
git add internal/proxy/proxy.go internal/proxy/codex_websocket_test.go internal/proxy/responses_stream_completion_test.go
git commit -m "fix(proxy): use websocket for Codex token pool streams"
```
