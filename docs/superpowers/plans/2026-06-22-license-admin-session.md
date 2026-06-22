# License Admin Session Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a logout-capable license admin session system with card history, card deletion, rate limiting, and updated admin UI controls.

**Architecture:** Extend `internal/onlinelicense` with session middleware, rate limit middleware, audit log listing, and card deletion. Keep `cmd/license-server/main.go` as the only HTML surface and call the existing JSON admin API from the page.

**Tech Stack:** Go `net/http`, in-memory session store, in-memory per-IP limiter, SQLite via `modernc.org/sqlite`, embedded HTML/CSS/JS.

---

### Task 1: Admin Session And Rate Limit Tests

**Files:**
- Modify: `internal/onlinelicense/http_test.go`

- [ ] Add tests showing `/api/admin/cards` rejects requests without a session.
- [ ] Add tests showing `POST /api/admin/login` sets an admin session cookie.
- [ ] Add tests showing `POST /api/admin/logout` invalidates that session.
- [ ] Add tests showing repeated failed login attempts return `429`.
- [ ] Run: `go test ./internal/onlinelicense -run 'TestAdmin' -count=1`
- [ ] Expected before implementation: tests fail because login/logout/session endpoints do not exist.

### Task 2: Admin Session And Rate Limit Implementation

**Files:**
- Modify: `internal/onlinelicense/http.go`
- Modify: `internal/onlinelicense/types.go`

- [ ] Add admin session creation, lookup, expiry, and logout.
- [ ] Add login/logout handlers.
- [ ] Update admin middleware to require the session cookie instead of Basic Auth.
- [ ] Add per-IP rate limiter helpers and apply them to login, public license calls, and admin mutation calls.
- [ ] Run: `go test ./internal/onlinelicense -run 'TestAdmin' -count=1`
- [ ] Expected after implementation: admin session tests pass.

### Task 3: Card History And Delete Tests

**Files:**
- Modify: `internal/onlinelicense/http_test.go`
- Modify: `internal/onlinelicense/service_test.go`

- [ ] Add tests showing deleting a card removes it from the card list and prevents future activation.
- [ ] Add tests showing audit history includes generate, activate, delete, login, and logout style records.
- [ ] Run: `go test ./internal/onlinelicense -run 'Test.*(Card|Audit|History)' -count=1`
- [ ] Expected before implementation: tests fail because delete and audit listing are missing.

### Task 4: Card History And Delete Implementation

**Files:**
- Modify: `internal/onlinelicense/store.go`
- Modify: `internal/onlinelicense/service.go`
- Modify: `internal/onlinelicense/http.go`
- Modify: `internal/onlinelicense/types.go`

- [ ] Add `AuditRecord` and store/service methods to list audit logs.
- [ ] Add store/service methods to delete a card and related activations.
- [ ] Add `DELETE /api/admin/cards/{id}` and `GET /api/admin/history`.
- [ ] Add audit writes for refresh, delete card, login, and logout.
- [ ] Run: `go test ./internal/onlinelicense -count=1`
- [ ] Expected after implementation: onlinelicense tests pass.

### Task 5: Admin UI Update

**Files:**
- Modify: `cmd/license-server/main.go`

- [ ] Replace Basic Auth page assumptions with login-aware UI.
- [ ] Add logout button.
- [ ] Add delete button next to card disable.
- [ ] Add history table.
- [ ] Add user-facing error handling for unauthorized and rate-limit responses.
- [ ] Run: `go test ./cmd/license-server ./internal/onlinelicense -count=1`
- [ ] Expected after implementation: license server builds and tests pass.

### Task 6: Final Verification

**Files:**
- None

- [ ] Run: `go test ./internal/onlinelicense ./cmd/license-server -count=1`
- [ ] Run: `go test ./... -count=1` if the focused test suite is clean and time allows.
- [ ] Check visible page text for clarity, button contrast, no em-dash characters, and mobile table overflow behavior.
