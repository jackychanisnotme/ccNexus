# Endpoint Rename Data Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make endpoint renames preserve Codex Token Pool credentials and all name-associated data in both desktop and server update flows.

**Architecture:** Add one transactional SQLite rename operation that updates the endpoint row and migrates every name-based association. Desktop and server callers retain the original name and invoke this operation only when the name changes; ordinary endpoint edits continue through the existing update path.

**Tech Stack:** Go 1.24+, `database/sql`, modernc SQLite, Wails desktop service, `net/http` server API, Go testing.

---

## File Map

- Modify `internal/storage/interface.go`: expose endpoint rename on the main storage interface.
- Modify `internal/storage/sqlite.go`: implement the atomic rename and data migration transaction.
- Modify `internal/storage/sqlite_test.go`: integration coverage for preservation, merging, collision, and rollback.
- Modify `internal/config/config.go`: expose rename through the configuration storage adapter contract.
- Modify `internal/config/config_test.go`: update the fake adapter to model explicit renames.
- Modify `internal/storage/adapter.go`: translate config endpoint data into the SQLite rename call.
- Modify `internal/service/endpoint.go`: use explicit rename before configuration synchronization.
- Create `internal/service/endpoint_rename_test.go`: desktop service regression coverage.
- Modify `cmd/server/webui/api/endpoints.go`: use the original route name when persisting a rename.
- Modify `cmd/server/webui/api/endpoints_proxy_test.go`: server API regression and active-name collision coverage.

### Task 1: Storage Rename Contract And Failing Integration Test

**Files:**
- Modify: `internal/storage/interface.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/storage/adapter.go`
- Test: `internal/storage/sqlite_test.go`

- [ ] **Step 1: Add the explicit rename contract**

Add to `storage.Storage`:

```go
RenameEndpoint(oldName string, ep *Endpoint) error
```

Add to `config.StorageAdapter`:

```go
RenameEndpoint(oldName string, ep *StorageEndpoint) error
```

Implement the adapter conversion in `internal/storage/adapter.go`:

```go
func (a *ConfigStorageAdapter) RenameEndpoint(oldName string, ep *config.StorageEndpoint) error {
	endpoint := &Endpoint{
		Name:        ep.Name,
		APIUrl:      ep.APIUrl,
		APIKey:      ep.APIKey,
		AuthMode:    ep.AuthMode,
		Enabled:     ep.Enabled,
		Transformer: ep.Transformer,
		Model:       ep.Model,
		Thinking:    ep.Thinking,
		ForceStream: ep.ForceStream,
		ProxyURL:    ep.ProxyURL,
		Remark:      ep.Remark,
		SortOrder:   ep.SortOrder,
	}
	return a.storage.RenameEndpoint(oldName, endpoint)
}
```

Update `fakeConfigStorage` with a rename implementation that finds `oldName`,
rejects a different existing destination name, and replaces the matching entry.

- [ ] **Step 2: Write a failing storage integration test**

Add `TestRenameEndpointPreservesAssociatedDataAndMergesHistory` to
`internal/storage/sqlite_test.go`. Seed:

```go
oldEndpoint := Endpoint{
	Name:        "Codex Old",
	APIUrl:      config.CodexTokenPoolAPIURL,
	AuthMode:    config.AuthModeCodexTokenPool,
	Enabled:     true,
	Transformer: config.CodexTokenPoolTransformer,
	Model:       config.CodexTokenPoolDefaultModel,
}
credential := EndpointCredential{
	EndpointName: "Codex Old",
	ProviderType: ProviderTypeCodex,
	AccessToken:  "access-token",
	Status:       "active",
	Enabled:      true,
}
```

Also seed a rate-limit snapshot, credential usage, runtime status, one
`daily_stats` row for `Codex Old`, and a matching date/device/client-IP row for
the stale historical name `Codex New`. Call:

```go
renamed := oldEndpoint
renamed.Name = "Codex New"
renamed.Remark = "renamed"
err := store.RenameEndpoint("Codex Old", &renamed)
```

Assert:

```go
if err != nil {
	t.Fatalf("rename endpoint: %v", err)
}
credentials, _ := store.GetEndpointCredentials("Codex New")
if len(credentials) != 1 || credentials[0].ID != credential.ID {
	t.Fatalf("renamed credentials = %#v, want original credential ID %d", credentials, credential.ID)
}
if oldCredentials, _ := store.GetEndpointCredentials("Codex Old"); len(oldCredentials) != 0 {
	t.Fatalf("old credentials remain: %#v", oldCredentials)
}
```

Verify the rate-limit snapshot remains available by the same credential ID,
credential usage is returned under `Codex New`, runtime status is returned only
under `Codex New`, the endpoint fields changed, and historical counters equal
the sum of the old and destination rows.

- [ ] **Step 3: Run the focused test and verify RED**

Run:

```bash
go test ./internal/storage -run TestRenameEndpointPreservesAssociatedDataAndMergesHistory -count=1 -v
```

Expected: build failure because `SQLiteStorage.RenameEndpoint` is not yet
implemented.

- [ ] **Step 4: Commit the red test and contract**

```bash
git add internal/storage/interface.go internal/config/config.go internal/config/config_test.go internal/storage/adapter.go internal/storage/sqlite_test.go
git commit -m "test: define endpoint rename migration contract"
```

### Task 2: Transactional SQLite Rename

**Files:**
- Modify: `internal/storage/sqlite.go`
- Test: `internal/storage/sqlite_test.go`

- [ ] **Step 1: Add transaction helpers**

Add private helpers near endpoint persistence:

```go
func rollbackUnlessCommitted(tx *sql.Tx, committed *bool) {
	if !*committed {
		_ = tx.Rollback()
	}
}
```

Keep the implementation local to storage and use parameterized SQL throughout.

- [ ] **Step 2: Implement active endpoint validation**

Start `RenameEndpoint` under `s.mu.Lock()`, normalize the endpoint, trim and
validate both names, and open a transaction:

```go
func (s *SQLiteStorage) RenameEndpoint(oldName string, ep *Endpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldName = strings.TrimSpace(oldName)
	ep.Name = strings.TrimSpace(ep.Name)
	if oldName == "" || ep.Name == "" {
		return fmt.Errorf("endpoint name is required")
	}
	if oldName == ep.Name {
		return fmt.Errorf("endpoint rename requires a different name")
	}
	normalizeEndpointAuthMode(ep)

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
```

Query the source endpoint and destination count. Return a descriptive error if
the source is missing or if another active endpoint row already uses `ep.Name`.

- [ ] **Step 3: Merge historical daily statistics**

Within the transaction, copy source rows to the destination key:

```sql
INSERT INTO daily_stats (
    endpoint_name, date, requests, errors, input_tokens, output_tokens,
    device_id, client_ip, created_at
)
SELECT ?, date, requests, errors, input_tokens, output_tokens,
       device_id, client_ip, created_at
FROM daily_stats
WHERE endpoint_name=?
ON CONFLICT(endpoint_name, date, device_id, client_ip) DO UPDATE SET
    requests=daily_stats.requests + excluded.requests,
    errors=daily_stats.errors + excluded.errors,
    input_tokens=daily_stats.input_tokens + excluded.input_tokens,
    output_tokens=daily_stats.output_tokens + excluded.output_tokens
```

Then delete only source-name history rows.

- [ ] **Step 4: Migrate credentials, usage, and runtime status**

Run:

```sql
UPDATE endpoint_credentials SET endpoint_name=?, updated_at=CURRENT_TIMESTAMP
WHERE endpoint_name=?
```

Run:

```sql
UPDATE credential_usage SET endpoint_name=?
WHERE endpoint_name=?
```

Merge source runtime status into a destination row using
`ON CONFLICT(endpoint_name) DO UPDATE`. For each status field, retain values
from whichever row has the later `updated_at`, then delete the source-name row.
Do not modify `credential_rate_limits`, because it follows stable credential
IDs.

- [ ] **Step 5: Update the endpoint row and commit**

Use `oldName` in the endpoint update predicate:

```sql
UPDATE endpoints SET
    name=?, api_url=?, api_key=?, auth_mode=?, enabled=?, transformer=?,
    model=?, thinking=?, force_stream=?, proxy_url=?, remark=?, sort_order=?,
    updated_at=CURRENT_TIMESTAMP
WHERE name=?
```

Require exactly one affected row. Commit and set `committed = true`.

- [ ] **Step 6: Run the focused test and verify GREEN**

Run:

```bash
go test ./internal/storage -run TestRenameEndpointPreservesAssociatedDataAndMergesHistory -count=1 -v
```

Expected: PASS.

- [ ] **Step 7: Add collision and rollback coverage**

Add `TestRenameEndpointRejectsActiveNameCollisionWithoutChanges`. Seed two
active endpoint rows and a credential for the source, attempt to rename the
source to the second endpoint's name, and assert:

```go
if err := store.RenameEndpoint("Source", &renamed); err == nil {
	t.Fatal("expected active endpoint name collision")
}
```

Verify both endpoint rows and the source credential remain unchanged.

- [ ] **Step 8: Run storage tests**

Run:

```bash
go test ./internal/storage -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit the storage implementation**

```bash
git add internal/storage/sqlite.go internal/storage/sqlite_test.go
git commit -m "fix: migrate endpoint data atomically on rename"
```

### Task 3: Desktop Endpoint Service Rename

**Files:**
- Modify: `internal/service/endpoint.go`
- Create: `internal/service/endpoint_rename_test.go`

- [ ] **Step 1: Write the failing desktop service regression test**

Create a temporary SQLite store, seed a Codex Token Pool endpoint and one
credential, load the endpoint into `config.Config`, and construct a real proxy:

```go
cfg := config.DefaultConfig()
cfg.UpdateEndpoints([]config.Endpoint{{
	Name:        "Codex Old",
	APIUrl:      config.CodexTokenPoolAPIURL,
	AuthMode:    config.AuthModeCodexTokenPool,
	Enabled:     true,
	Transformer: config.CodexTokenPoolTransformer,
	Model:       config.CodexTokenPoolDefaultModel,
}})
p := proxy.New(cfg, nil, store, "test-device")
service := NewEndpointService(cfg, p, store)
```

Call `service.UpdateEndpoint` with name `Codex New`, then assert the credential
still exists under `Codex New` with its original ID and no credential remains
under `Codex Old`.

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
go test ./internal/service -run TestUpdateEndpointRenamePreservesTokenPool -count=1 -v
```

Expected: FAIL because the existing save flow deletes the old endpoint and its
credentials.

- [ ] **Step 3: Persist an explicit rename before configuration sync**

In `EndpointService.UpdateEndpoint`, after validation and before
`SaveToStorage`, add:

```go
if e.storage != nil && oldName != updatedEndpoint.Name {
	configAdapter := storage.NewConfigStorageAdapter(e.storage)
	if err := configAdapter.RenameEndpoint(oldName, &config.StorageEndpoint{
		Name:        updatedEndpoint.Name,
		APIUrl:      updatedEndpoint.APIUrl,
		APIKey:      updatedEndpoint.APIKey,
		AuthMode:    updatedEndpoint.AuthMode,
		Enabled:     updatedEndpoint.Enabled,
		Transformer: updatedEndpoint.Transformer,
		Model:       updatedEndpoint.Model,
		Thinking:    updatedEndpoint.Thinking,
		ForceStream: updatedEndpoint.ForceStream,
		ProxyURL:    updatedEndpoint.ProxyURL,
		Remark:      updatedEndpoint.Remark,
		SortOrder:   index,
	}); err != nil {
		return fmt.Errorf("failed to rename endpoint: %w", err)
	}
}
```

Then retain the existing `SaveToStorage` pass so other endpoint ordering and
configuration fields are synchronized.

- [ ] **Step 4: Run the desktop regression test and service package**

Run:

```bash
go test ./internal/service -run TestUpdateEndpointRenamePreservesTokenPool -count=1 -v
go test ./internal/service -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the desktop path**

```bash
git add internal/service/endpoint.go internal/service/endpoint_rename_test.go
git commit -m "fix: preserve token pool during desktop endpoint rename"
```

### Task 4: Server Web API Rename

**Files:**
- Modify: `cmd/server/webui/api/endpoints.go`
- Modify: `cmd/server/webui/api/endpoints_proxy_test.go`

- [ ] **Step 1: Write the failing server API regression test**

Add `TestEndpointAPIRenamePreservesTokenPool` using `newAPITestStorage`. Seed a
Codex endpoint and credential, then issue:

```go
postJSON(t, handler, http.MethodPut, "/api/endpoints/Codex%20Old", map[string]any{
	"name":        "Codex New",
	"apiUrl":      config.CodexTokenPoolAPIURL,
	"authMode":    config.AuthModeCodexTokenPool,
	"enabled":     true,
	"transformer": config.CodexTokenPoolTransformer,
	"model":       config.CodexTokenPoolDefaultModel,
})
```

Assert the renamed endpoint is stored and the credential ID remains available
under `Codex New`.

- [ ] **Step 2: Run the API test and verify RED**

Run:

```bash
go test ./cmd/server/webui/api -run TestEndpointAPIRenamePreservesTokenPool -count=1 -v
```

Expected: FAIL because `UpdateEndpoint` searches by the new name and does not
persist the rename.

- [ ] **Step 3: Use the original route name for persistence**

Keep `name` as `oldName` before mutating `existing.Name`. At persistence:

```go
if oldName != existing.Name {
	if err := h.storage.RenameEndpoint(oldName, existing); err != nil {
		logger.Error("Failed to rename endpoint: %v", err)
		WriteError(w, http.StatusConflict, err.Error())
		return
	}
} else if err := h.storage.UpdateEndpoint(existing); err != nil {
	logger.Error("Failed to update endpoint: %v", err)
	WriteError(w, http.StatusInternalServerError, "Failed to update endpoint")
	return
}
```

The storage transaction owns active-name collision validation. Return HTTP 409
for rename conflicts.

- [ ] **Step 4: Add API collision coverage**

Seed active endpoints `Source` and `Destination`; attempt to rename `Source` to
`Destination`; assert HTTP 409 and verify both endpoints still exist unchanged.
Use a direct recorder rather than `postJSON`, because `postJSON` requires 200.

- [ ] **Step 5: Run API tests**

Run:

```bash
go test ./cmd/server/webui/api -run 'TestEndpointAPI(RenamePreservesTokenPool|RenameRejectsActiveCollision)' -count=1 -v
go test ./cmd/server/webui/api -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the server path**

```bash
git add cmd/server/webui/api/endpoints.go cmd/server/webui/api/endpoints_proxy_test.go
git commit -m "fix: preserve endpoint data during server rename"
```

### Task 5: Full Verification

**Files:**
- Verify all modified Go files.

- [ ] **Step 1: Format changed Go files**

Run:

```bash
gofmt -w internal/storage/interface.go internal/storage/sqlite.go internal/storage/sqlite_test.go internal/config/config.go internal/config/config_test.go internal/storage/adapter.go internal/service/endpoint.go internal/service/endpoint_rename_test.go cmd/server/webui/api/endpoints.go cmd/server/webui/api/endpoints_proxy_test.go
```

- [ ] **Step 2: Run focused regression packages**

Run:

```bash
go test ./internal/storage ./internal/config ./internal/service ./cmd/server/webui/api -count=1
```

Expected: PASS.

- [ ] **Step 3: Run all tests**

Run:

```bash
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 4: Run static analysis**

Run:

```bash
go vet ./...
```

Expected: exit status 0.

- [ ] **Step 5: Inspect the final diff**

Run:

```bash
git diff --check
git status --short
git diff -- internal/storage/interface.go internal/storage/sqlite.go internal/storage/sqlite_test.go internal/config/config.go internal/config/config_test.go internal/storage/adapter.go internal/service/endpoint.go internal/service/endpoint_rename_test.go cmd/server/webui/api/endpoints.go cmd/server/webui/api/endpoints_proxy_test.go
```

Expected: no whitespace errors; only the intended rename migration changes plus
pre-existing user edits are present.

- [ ] **Step 6: Commit any final formatting-only changes**

If formatting changed tracked implementation files after the task commits:

```bash
git add internal/storage/interface.go internal/storage/sqlite.go internal/storage/sqlite_test.go internal/config/config.go internal/config/config_test.go internal/storage/adapter.go internal/service/endpoint.go internal/service/endpoint_rename_test.go cmd/server/webui/api/endpoints.go cmd/server/webui/api/endpoints_proxy_test.go
git commit -m "style: format endpoint rename migration"
```
