# Tool Search Call Arguments Normalization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Normalize valid JSON object strings in Responses `tool_search_call.arguments` before AINexus writes responses to Codex.

**Architecture:** Add a proxy-local response-boundary normalizer for plain JSON and SSE `data:` payloads. Invoke it after transformer output for non-streaming and streaming responses while leaving standard `function_call` items and malformed values unchanged.

**Tech Stack:** Go 1.24, `encoding/json`, `net/http/httptest`, Go test.

---

### Task 1: Specify normalization behavior

**Files:**
- Create: `internal/proxy/responses_normalization_test.go`

- [ ] **Step 1: Add table-driven JSON tests**

Create `TestNormalizeOpenAIResponsesToolSearchArgumentsJSON` with direct output items covering:

```go
tests := []struct {
	name        string
	payload     string
	wantChanged bool
	wantType    string
}{
	{"tool search object string", `{"type":"tool_search_call","arguments":"{\"query\":\"logs\",\"limit\":8}"}`, true, "object"},
	{"tool search object", `{"type":"tool_search_call","arguments":{"query":"logs"}}`, false, "object"},
	{"tool search array string", `{"type":"tool_search_call","arguments":"[]"}`, false, "string"},
	{"tool search invalid string", `{"type":"tool_search_call","arguments":"{"}`, false, "string"},
	{"function call string", `{"type":"function_call","arguments":"{\"query\":\"logs\"}"}`, false, "string"},
}
```

Call `normalizeOpenAIResponsesToolSearchArguments`, decode the returned JSON, and assert both `changed` and the resulting argument type.

- [ ] **Step 2: Add SSE container tests**

Add `TestNormalizeOpenAIResponsesToolSearchArgumentsSSEOutputItem` for `response.output_item.done` and `TestNormalizeOpenAIResponsesToolSearchArgumentsSSECompleted` for `response.completed.response.output`. Parse each returned `data:` line and assert that `arguments` is an object.

- [ ] **Step 3: Verify RED**

```bash
go test ./internal/proxy -run 'TestNormalizeOpenAIResponsesToolSearchArguments' -count=1
```

Expected: build failure because `normalizeOpenAIResponsesToolSearchArguments` does not exist.

### Task 2: Implement the response normalizer

**Files:**
- Create: `internal/proxy/responses_normalization.go`
- Test: `internal/proxy/responses_normalization_test.go`

- [ ] **Step 1: Add the minimal API**

```go
func normalizeOpenAIResponsesToolSearchArguments(payload []byte, streaming bool) ([]byte, bool)
```

For JSON, unmarshal into `map[string]interface{}` and visit only a direct item, top-level `item`, top-level `output`, and nested `response`. For an item whose `type` is `tool_search_call`, parse string `arguments` into `map[string]interface{}` and replace it only when the parsed map is non-nil. For SSE, rewrite only JSON-bearing `data:` lines and preserve event/control lines and `[DONE]`. Return the original bytes and `false` when unchanged.

- [ ] **Step 2: Verify GREEN**

```bash
go test ./internal/proxy -run 'TestNormalizeOpenAIResponsesToolSearchArguments' -count=1
```

Expected: PASS.

### Task 3: Integrate both response paths

**Files:**
- Modify: `internal/proxy/response.go`
- Modify: `internal/proxy/streaming.go`
- Modify: `internal/proxy/responses_normalization_test.go`

- [ ] **Step 1: Add failing non-streaming integration test**

Use `handleNonStreamingResponse` with the passthrough Responses transformer. Return a Response object containing string-typed `tool_search_call.arguments` and assert the recorder body contains an object.

- [ ] **Step 2: Add failing streaming integration test**

Use `handleStreamingResponse` with `response.output_item.done` and `response.completed` events containing string-typed arguments. Assert both downstream `data:` payloads contain argument objects.

- [ ] **Step 3: Verify RED**

```bash
go test ./internal/proxy -run 'TestHandle.*NormalizesToolSearchCallArguments' -count=1
```

Expected: FAIL because transformed responses are still written unchanged.

- [ ] **Step 4: Apply normalization after transformation**

In `handleNonStreamingResponse`, normalize `transformedResp` immediately after `TransformResponse`. In `transformStreamEvent`, normalize successful transformer output in SSE mode before returning it to observation and downstream writing.

- [ ] **Step 5: Format and verify focused tests**

```bash
gofmt -w internal/proxy/responses_normalization.go internal/proxy/responses_normalization_test.go internal/proxy/response.go internal/proxy/streaming.go
go test ./internal/proxy -run 'TestNormalizeOpenAIResponsesToolSearchArguments|TestHandle.*NormalizesToolSearchCallArguments' -count=1
```

Expected: PASS.

### Task 4: Verify the repository

**Files:**
- Verify: `internal/proxy/responses_normalization.go`
- Verify: `internal/proxy/responses_normalization_test.go`
- Verify: `internal/proxy/response.go`
- Verify: `internal/proxy/streaming.go`

- [ ] **Step 1: Run package tests**

```bash
go test ./internal/proxy ./internal/transformer/convert -count=1
```

- [ ] **Step 2: Run complete checks**

```bash
go test ./... -count=1
go vet ./...
git diff --check
```

- [ ] **Step 3: Review scope**

Confirm `git status --short` contains only this plan, the normalizer, tests, and the two proxy integration files in addition to the user's pre-existing desktop resource changes. Do not stage or modify the pre-existing files.
