# OpenAI2 Chat Completed Output Reconciliation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent OpenAI Responses `response.completed` events from duplicating text and tool calls already emitted to Chat Completions clients when the final output array omits earlier items and compacts its indices.

**Architecture:** Keep the Responses passthrough path unchanged. In the Responses-to-Chat stream converter, retain real stream item identities and reconcile completed items against the original stream index by message item ID, tool `call_id`, or a conservative text-prefix fallback.

**Tech Stack:** Go 1.24+, `encoding/json`, Go `testing`

---

## File Structure

- Modify `internal/transformer/types.go`: decode the Responses stream `item_id`.
- Modify `internal/transformer/convert/common.go`: record item identities and resolve completed items to their original stream indices.
- Modify `internal/transformer/convert/openai_openai2.go`: use identity reconciliation while converting `response.completed`.
- Modify `internal/transformer/convert/openai_openai2_test.go`: regression coverage for compacted final output.

### Task 1: Add failing compacted-output regressions

**Files:**
- Test: `internal/transformer/convert/openai_openai2_test.go`

- [ ] **Step 1: Add a regression with stable item IDs**

Feed text at `output_index=1` and a completed function call at `output_index=2`, then send a final output array containing only the message and function call. Assert that the final Chat delta contains neither repeated content nor repeated `tool_calls`.

- [ ] **Step 2: Add a missing-message-ID fallback regression**

Feed text at `output_index=1` without an `item_id`, then send the same full text at compacted final array index `0`. Assert that the final Chat delta does not repeat the text.

- [ ] **Step 3: Verify RED**

Run:

```bash
go test ./internal/transformer/convert -run 'TestOpenAI2StreamToOpenAI.*CompactedCompleted' -count=1 -v
```

Expected: FAIL because the final Chat chunk repeats previously streamed content and tool calls.

### Task 2: Reconcile completed output with stream identity

**Files:**
- Modify: `internal/transformer/types.go`
- Modify: `internal/transformer/convert/common.go`
- Modify: `internal/transformer/convert/openai_openai2.go`

- [ ] **Step 1: Decode and record stream item IDs**

Add `ItemID string` with JSON key `item_id` to `OpenAI2StreamEvent`. Record non-empty item IDs from text, content-part, argument, added, and done events against their actual `output_index`.

- [ ] **Step 2: Resolve completed items**

Add a helper that resolves a completed item to an existing stream index in this order:

```text
message/function item ID
function call_id
longest streamed text prefix
completed array position
```

- [ ] **Step 3: Use resolved indices**

Use the resolved index for completed tool deduplication and completed text suffix calculation. Preserve completed-only output behavior when no prior stream state exists.

- [ ] **Step 4: Verify GREEN**

Run:

```bash
go test ./internal/transformer/convert -run 'TestOpenAI2StreamToOpenAI.*CompactedCompleted|TestOpenAI2StreamToOpenAIEmitsCompletedOnly' -count=1 -v
```

Expected: PASS.

### Task 3: Verify compatibility

**Files:**
- Modify: `internal/transformer/types.go`
- Modify: `internal/transformer/convert/common.go`
- Modify: `internal/transformer/convert/openai_openai2.go`
- Modify: `internal/transformer/convert/openai_openai2_test.go`

- [ ] **Step 1: Format**

```bash
gofmt -w internal/transformer/types.go internal/transformer/convert/common.go internal/transformer/convert/openai_openai2.go internal/transformer/convert/openai_openai2_test.go
```

- [ ] **Step 2: Run converter and proxy tests**

```bash
go test ./internal/transformer/convert ./internal/proxy -count=1
```

Expected: PASS.

- [ ] **Step 3: Run all Go tests**

```bash
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 4: Inspect scope**

```bash
git diff --check
git diff -- internal/transformer/types.go internal/transformer/convert/common.go internal/transformer/convert/openai_openai2.go internal/transformer/convert/openai_openai2_test.go
```

Expected: no whitespace errors and no changes to the Responses passthrough transformer.
