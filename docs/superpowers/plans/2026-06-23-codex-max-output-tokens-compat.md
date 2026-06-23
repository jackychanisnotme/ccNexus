# Codex Token Pool `max_output_tokens` Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent Codex Token Pool requests from failing when a Responses client sends `max_output_tokens`, without changing other providers.

**Architecture:** Keep generic Responses normalization unchanged. Remove the unsupported top-level field in the existing request-boundary helper that runs only for the ChatGPT Codex Responses backend.

**Tech Stack:** Go 1.24+, `net/http`, `encoding/json`, Go `testing`

---

## File Structure

- Modify `internal/proxy/request_test.go`: regression and isolation coverage.
- Modify `internal/proxy/request.go`: Codex-only payload normalization.

### Task 1: Add failing Codex compatibility regression

**Files:**
- Test: `internal/proxy/request_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestEnsureCodexResponsesPayloadRemovesMaxOutputTokens(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.5","max_output_tokens":2048,"input":"hello","metadata":{"source":"agent"}}`)
	out := ensureCodexResponsesPayload(raw)

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, ok := payload["max_output_tokens"]; ok {
		t.Fatalf("expected max_output_tokens to be removed, got %#v", payload["max_output_tokens"])
	}
	if payload["input"] != "hello" {
		t.Fatalf("expected input to be preserved, got %#v", payload["input"])
	}
	metadata, ok := payload["metadata"].(map[string]interface{})
	if !ok || metadata["source"] != "agent" {
		t.Fatalf("expected metadata to be preserved, got %#v", payload["metadata"])
	}
}
```

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/proxy -run '^TestEnsureCodexResponsesPayloadRemovesMaxOutputTokens$' -count=1`

Expected: FAIL because `max_output_tokens` remains present.

### Task 2: Implement the minimal Codex-only fix

**Files:**
- Modify: `internal/proxy/request.go:353`

- [ ] **Step 1: Remove the unsupported field**

Insert after successful JSON object parsing:

```go
delete(body, "max_output_tokens")
body["store"] = false
body["stream"] = true
```

Keep all other behavior unchanged.

- [ ] **Step 2: Verify GREEN**

Run: `go test ./internal/proxy -run '^TestEnsureCodexResponsesPayloadRemovesMaxOutputTokens$' -count=1`

Expected: PASS.

### Task 3: Prove non-Codex compatibility

**Files:**
- Test: `internal/proxy/request_test.go`

- [ ] **Step 1: Add the isolation test**

```go
func TestBuildProxyRequestPreservesMaxOutputTokensForNonCodexResponses(t *testing.T) {
	endpoint := config.Endpoint{
		Name: "OpenAI Responses", APIUrl: "https://api.example.com",
		AuthMode: config.AuthModeAPIKey, Transformer: "openai2", Model: "gpt-5.5",
	}
	body := []byte(`{"model":"gpt-5.5","max_output_tokens":2048,"input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))

	proxyReq, err := buildProxyRequest(req, endpoint, "test-key", body, "cx_resp_openai2", nil)
	if err != nil {
		t.Fatalf("buildProxyRequest failed: %v", err)
	}
	defer proxyReq.Body.Close()
	upstreamBody, err := io.ReadAll(proxyReq.Body)
	if err != nil {
		t.Fatalf("read upstream body failed: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(upstreamBody, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if payload["max_output_tokens"] != float64(2048) {
		t.Fatalf("expected max_output_tokens to be preserved, got %#v", payload["max_output_tokens"])
	}
}
```

- [ ] **Step 2: Run both compatibility tests**

Run: `go test ./internal/proxy -run 'TestEnsureCodexResponsesPayloadRemovesMaxOutputTokens|TestBuildProxyRequestPreservesMaxOutputTokensForNonCodexResponses' -count=1`

Expected: PASS for both.

### Task 4: Verify and commit

**Files:**
- Modify: `internal/proxy/request.go`
- Modify: `internal/proxy/request_test.go`

- [ ] **Step 1: Format**

Run: `gofmt -w internal/proxy/request.go internal/proxy/request_test.go`

- [ ] **Step 2: Run package tests**

Run: `go test ./internal/proxy -count=1`

Expected: PASS.

- [ ] **Step 3: Run all Go tests**

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 4: Inspect**

Run: `git diff --check` and `git diff -- internal/proxy/request.go internal/proxy/request_test.go`.

Expected: no whitespace errors and only the scoped compatibility changes.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/request.go internal/proxy/request_test.go docs/superpowers/plans/2026-06-23-codex-max-output-tokens-compat.md
git commit -m "fix(proxy): omit output token limit for Codex pool"
```
