# macOS Menu Bar Icon Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the white-tile macOS menu bar icon with a transparent monochrome routing-hub template image while preserving the full-color application and Windows tray icons.

**Architecture:** Add a dedicated transparent PNG for the macOS status item and embed it separately from the application icon. Select that asset only on macOS, and mark the decoded `NSImage` as a template image so AppKit automatically adapts it to light and dark menu bars.

**Tech Stack:** Go embed, Objective-C/AppKit, Pillow-generated PNG, Go tests, source-contract tests.

---

## File Structure

- `cmd/desktop/build/trayicon-macos.png`: transparent monochrome routing-hub menu bar asset.
- `cmd/desktop/main.go`: embed and select the macOS-specific tray asset.
- `internal/tray/tray_darwin.m`: mark the status item image as an AppKit template image.
- `cmd/desktop/tray_icon_test.go`: verify platform-specific embed and selection contracts.
- `internal/tray/tray_darwin_source_test.go`: verify AppKit template-image behavior.

### Task 1: Define The macOS Tray Contract

**Files:**
- Create: `cmd/desktop/tray_icon_test.go`
- Create: `internal/tray/tray_darwin_source_test.go`

- [ ] **Step 1: Write the failing Go source-contract tests**

`cmd/desktop/tray_icon_test.go`:

```go
package main

import (
    "os"
    "strings"
    "testing"
)

func TestMainEmbedsDedicatedMacOSTrayIcon(t *testing.T) {
    source, err := os.ReadFile("main.go")
    if err != nil {
        t.Fatal(err)
    }
    text := string(source)

    if !strings.Contains(text, "//go:embed build/trayicon-macos.png") {
        t.Fatal("main.go must embed the dedicated macOS tray icon")
    }
    if !strings.Contains(text, "runtime.GOOS == \"darwin\"") {
        t.Fatal("main.go must select the dedicated tray icon on macOS")
    }
}
```

`internal/tray/tray_darwin_source_test.go`:

```go
package tray

import (
    "os"
    "strings"
    "testing"
)

func TestDarwinTrayUsesTemplateImage(t *testing.T) {
    source, err := os.ReadFile("tray_darwin.m")
    if err != nil {
        t.Fatal(err)
    }
    if !strings.Contains(string(source), "[icon setTemplate:YES]") {
        t.Fatal("macOS tray icon must be marked as a template image")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./cmd/desktop ./internal/tray -count=1
```

Expected: FAIL because the dedicated embed, Darwin selection, and template-image call do not exist.

- [ ] **Step 3: Commit the failing tests**

```bash
git add cmd/desktop/tray_icon_test.go internal/tray/tray_darwin_source_test.go
git commit -m "test: define macOS tray icon contract"
```

### Task 2: Create The Transparent Menu Bar Asset

**Files:**
- Create: `cmd/desktop/build/trayicon-macos.png`

- [ ] **Step 1: Generate a transparent monochrome routing-hub icon**

Use the bundled Pillow runtime:

```bash
/Users/pc/.cache/codex-runtimes/codex-primary-runtime/dependencies/python/bin/python3 - <<'PY'
from pathlib import Path
from PIL import Image, ImageDraw

root = Path.cwd()
size = 72
image = Image.new("RGBA", (size, size), (0, 0, 0, 0))
draw = ImageDraw.Draw(image)
ink = (0, 0, 0, 255)

center = (36, 36)
draw.rounded_rectangle((27, 27, 45, 45), radius=5, fill=ink)

channels = [
    (2, 31, 29, 36),
    (43, 36, 70, 41),
    (31, 2, 36, 29),
    (36, 43, 41, 70),
    (8, 11, 30, 32),
    (42, 40, 64, 61),
]
for box in channels:
    draw.rounded_rectangle(box, radius=3, fill=ink)

image.save(root / "cmd/desktop/build/trayicon-macos.png", optimize=True)
PY
```

- [ ] **Step 2: Verify transparency and dimensions**

Run:

```bash
/Users/pc/.cache/codex-runtimes/codex-primary-runtime/dependencies/python/bin/python3 - <<'PY'
from PIL import Image

image = Image.open("cmd/desktop/build/trayicon-macos.png").convert("RGBA")
alpha = image.getchannel("A")
assert image.size == (72, 72)
assert alpha.getextrema() == (0, 255)
print(image.size, alpha.getextrema())
PY
```

Expected: `(72, 72) (0, 255)`.

- [ ] **Step 3: Commit the asset**

```bash
git add cmd/desktop/build/trayicon-macos.png
git commit -m "assets: add macOS menu bar template icon"
```

### Task 3: Wire The Dedicated macOS Tray Icon

**Files:**
- Modify: `cmd/desktop/main.go`
- Modify: `internal/tray/tray_darwin.m`
- Test: `cmd/desktop/tray_icon_test.go`
- Test: `internal/tray/tray_darwin_source_test.go`

- [ ] **Step 1: Embed and select the macOS asset**

Add the standard library runtime import:

```go
import "runtime"
```

Embed the asset:

```go
//go:embed build/trayicon-macos.png
var trayIconMacOS []byte
```

Select by platform:

```go
var trayIcon []byte
if runtime.GOOS == "windows" {
    trayIcon = trayIconWindows
} else if runtime.GOOS == "darwin" {
    trayIcon = trayIconMacOS
} else {
    trayIcon = trayIconOther
}
```

- [ ] **Step 2: Mark the AppKit image as a template**

After decoding the image in `tray_darwin.m`, add:

```objective-c
[icon setTemplate:YES];
```

Keep the existing 18-point size and proportional scaling.

- [ ] **Step 3: Run focused tests**

Run:

```bash
go test ./cmd/desktop ./internal/tray -count=1
```

Expected: PASS.

- [ ] **Step 4: Run full verification**

Run:

```bash
cd cmd/desktop/frontend
npm run build
cd ../../..
go test ./... -count=1
```

Expected: frontend build succeeds and all Go tests pass.

- [ ] **Step 5: Commit the integration**

```bash
git add cmd/desktop/main.go internal/tray/tray_darwin.m
git commit -m "fix: use template icon in macOS menu bar"
```

### Task 4: Visual Verification

- [ ] **Step 1: Inspect the asset at menu bar scale**

Render or preview the PNG at 18x18 and confirm:

- no opaque white or colored square exists;
- the routing-hub silhouette remains recognizable;
- the channels do not collapse into a solid block;
- the icon has transparent padding around the symbol.

- [ ] **Step 2: Verify AppKit behavior when a Wails runtime is available**

Run:

```bash
cd cmd/desktop
wails dev
```

Expected: the menu bar displays a monochrome symbol without a white tile and adapts to the current macOS appearance.

If the `wails` command is unavailable, record that limitation and rely on the transparent-alpha and template-image contract checks.
