# AINexus Rename Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename the product to AINexus while preserving AINexus compatibility for existing user data, environment variables, and old WebDAV/local-state paths.

**Architecture:** Add a small branding/paths helper so the server, desktop app, WebDAV client, and updater all agree on the new name, data directory, and filenames. Keep legacy `AINexus` environment variables and old on-disk locations as fallbacks so upgrades continue to open existing data without manual migration.

**Tech Stack:** Go 1.24, Wails v2, vanilla JS frontend, shell-based verification with `go test` and `go build`.

---

### Task 1: Add branding and compatibility helpers

**Files:**
- Create: `internal/branding/branding.go`
- Create: `internal/branding/branding_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestResolveDataDirPrefersNewDirButFallsBackToLegacy(t *testing.T) { ... }
func TestLookupEnvPrefersPrimaryOverLegacy(t *testing.T) { ... }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/branding -v`
Expected: FAIL because the helper package does not exist yet.

- [ ] **Step 3: Write minimal implementation**

```go
package branding

const Name = "AINexus"
const LegacyName = "AINexus"

func LookupEnv(primary, legacy string) string { ... }
func DefaultDataDir(home string) string { ... }
func LegacyDataDir(home string) string { ... }
func ResolveDataDir(home string) string { ... }
func DatabaseFilename() string { return "ainexus.db" }
func LegacyDatabaseFilename() string { return "ainexus.db" }
func WebDAVConfigPath() string { return "/AINexus/config" }
func WebDAVStatsPath() string { return "/AINexus/stats" }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/branding -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/branding
git commit -m "feat: add AINexus branding helpers"
```

### Task 2: Migrate server and desktop runtime paths

**Files:**
- Modify: `cmd/server/main.go`
- Modify: `cmd/desktop/main.go`
- Modify: `cmd/desktop/app.go`
- Modify: `internal/service/webdav.go`
- Modify: `internal/webdav/client.go`
- Modify: `internal/service/backup_helpers.go`

- [ ] **Step 1: Write the failing test**

```go
func TestResolveDataDirFallsBackToLegacyHomeDir(t *testing.T) { ... }
func TestApplyEnvOverridesSupportsAINexusEnvVars(t *testing.T) { ... }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/server ./cmd/desktop ./internal/service ./internal/webdav -v`
Expected: FAIL until the new helper and env handling are wired in.

- [ ] **Step 3: Write minimal implementation**

```go
dbPath := branding.ResolveDatabasePath(homeDir, dataDir)
homeDir := branding.ResolveDataDir(homeDir)
port := branding.LookupEnv("AINEXUS_PORT", "CCNEXUS_PORT")
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/server ./cmd/desktop ./internal/service ./internal/webdav -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/server cmd/desktop internal/service internal/webdav
git commit -m "feat: migrate runtime paths to AINexus"
```

### Task 3: Update visible branding and frontend state keys

**Files:**
- Modify: `cmd/server/webui/ui/index.html`
- Modify: `cmd/server/webui/ui/js/*`
- Modify: `cmd/desktop/frontend/src/**/*`
- Modify: `cmd/desktop/wails.json`
- Modify: `cmd/desktop/build/darwin/Info.plist`
- Modify: `cmd/desktop/build/windows/wails.exe.manifest`
- Modify: `internal/proxy/*.go`
- Modify: `internal/service/*.go`
- Modify: `internal/updater/*.go`

- [ ] **Step 1: Write the failing test**

```go
func TestUpdaterUsesAINexusAssetNames(t *testing.T) { ... }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/updater ./internal/proxy ./internal/service -v`
Expected: FAIL until brand strings are updated.

- [ ] **Step 3: Write minimal implementation**

```js
const NEW_KEY = 'AINexus_endpointTestStatus';
const OLD_KEY = 'AINexus_endpointTestStatus';
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/updater ./internal/proxy ./internal/service -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/server/webui cmd/desktop/frontend cmd/desktop/wails.json cmd/desktop/build internal/proxy internal/service internal/updater
git commit -m "feat: rename product branding to AINexus"
```

### Task 4: Update docs and release metadata

**Files:**
- Modify: `README.md`
- Modify: `docs/*.md`
- Modify: `CLAUDE.md`
- Modify: `AGENTS.md`
- Modify: `SOURCE-恢复说明.md`
- Modify: `patches/*.patch`
- Modify: `.github/workflows/build.yml`
- Modify: `.gitignore`

- [ ] **Step 1: Write the failing test**

```bash
rg -n "AINexus|ainexus|CCNEXUS" README.md docs CLAUDE.md AGENTS.md SOURCE-恢复说明.md .github/workflows/build.yml .gitignore
```

- [ ] **Step 2: Run test to verify it fails**

Run: `rg -n "AINexus|ainexus|CCNEXUS" ...`
Expected: residual matches to review and intentionally keep only for compatibility notes.

- [ ] **Step 3: Write minimal implementation**

Replace visible branding with `AINexus`, keep compatibility references where needed, and update artifact names to `AINexus-*`.

- [ ] **Step 4: Run test to verify it passes**

Run: `rg -n "AINexus|ainexus|CCNEXUS" ...`
Expected: only deliberate compatibility mentions remain.

- [ ] **Step 5: Commit**

```bash
git add README.md docs CLAUDE.md AGENTS.md SOURCE-恢复说明.md patches .github/workflows/build.yml .gitignore
git commit -m "docs: rename project to AINexus"
```

### Task 5: Verify the rename end to end

**Files:**
- All changed files

- [ ] **Step 1: Format code**

Run: `go fmt ./...`
Expected: no formatting diffs remain.

- [ ] **Step 2: Run tests**

Run: `go test ./... -count=1`
Expected: all tests pass.

- [ ] **Step 3: Build key targets**

Run: `cd cmd/server && go build ./...`
Run: `cd cmd/desktop && go build ./...`
Expected: both builds succeed.

- [ ] **Step 4: Review remaining AINexus mentions**

Run: `rg -n "AINexus|ainexus|CCNEXUS" -S -g '!**/node_modules/**'`
Expected: only legacy compatibility strings, historical notes, or upstream references remain.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: complete AINexus rename"
```
