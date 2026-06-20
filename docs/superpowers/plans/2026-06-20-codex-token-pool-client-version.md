# Codex Token Pool Client Version Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Codex token-pool endpoint tests and proxy traffic identify as stable Codex CLI 0.141.0 so `gpt-5.5` requests are accepted upstream.

**Architecture:** Export one compatibility identity from `internal/config` and consume it in the endpoint-test and proxy request paths. Behavior-level tests assert the actual headers produced by both paths, preventing the two callers from drifting apart.

**Tech Stack:** Go 1.24, `net/http`, standard Go testing

---

### Task 1: Add Failing Compatibility Header Tests

**Files:**
- Modify: `internal/service/endpoint_proxy_test.go`
- Modify: `internal/proxy/request_test.go`

- [ ] **Step 1: Test the endpoint-test header behavior**

Add a test that builds a `/responses` request, calls `applyCodexCredentialHeadersForTest`, and asserts `Version == "0.141.0"` and `User-Agent` contains `codex_cli_rs/0.141.0`.

- [ ] **Step 2: Test the production proxy header behavior**

Add a test that builds a Codex token-pool proxy request using `buildProxyRequest` and asserts the same version and user-agent values on the outgoing request.

- [ ] **Step 3: Run the focused tests and verify RED**

Run:

```bash
go test ./internal/service ./internal/proxy -run 'Test.*Codex.*StableClientIdentity' -count=1
```

Expected: both tests fail because the outgoing headers still contain `0.101.0`.

### Task 2: Centralize and Upgrade the Codex Identity

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/service/endpoint.go`
- Modify: `internal/proxy/request.go`
- Modify: `internal/proxy/codex_rate_limits.go`

- [ ] **Step 1: Add the shared identity**

Add these constants beside the existing Codex token-pool constants:

```go
CodexClientVersion = "0.141.0"
CodexUserAgent     = "codex_cli_rs/0.141.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464"
```

- [ ] **Step 2: Replace endpoint-test constants**

Remove `codexTestClientVersion` and `codexTestUserAgent`, then use `config.CodexClientVersion` and `config.CodexUserAgent` in `applyCodexCredentialHeadersForTest`.

- [ ] **Step 3: Replace proxy constants**

Remove `codexClientVersion` and `codexUserAgent`, then use the shared config constants in `applyCodexCredentialHeaders` and `fetchCodexRateLimitsForCredential`.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run:

```bash
go test ./internal/service ./internal/proxy -run 'Test.*Codex.*StableClientIdentity' -count=1
```

Expected: PASS.

### Task 3: Verify the Repository

**Files:**
- Modify mechanically as needed: files changed in Tasks 1-2

- [ ] **Step 1: Format changed Go files**

Run:

```bash
gofmt -w internal/config/config.go internal/service/endpoint.go internal/service/endpoint_proxy_test.go internal/proxy/request.go internal/proxy/request_test.go internal/proxy/codex_rate_limits.go
```

- [ ] **Step 2: Run affected packages**

Run:

```bash
go test ./internal/config ./internal/service ./internal/proxy -count=1
```

Expected: PASS.

- [ ] **Step 3: Run all Go tests**

Run:

```bash
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 4: Review the final diff**

Run `git diff --check` and inspect `git diff` to confirm only the shared identity, its callers, and regression tests changed.
