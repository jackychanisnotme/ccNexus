# AINexus App Icon Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the packaged desktop icon, tray icon, Windows icon, and desktop header rocket with the selected light Radial Hub artwork.

**Architecture:** Keep all Wails asset paths unchanged so packaging and runtime embed behavior continue to work. Add one frontend public PNG for the header, reference it from the existing UI template, and add focused source-level tests for the new header contract.

**Tech Stack:** GPT Image 2 source PNG, Pillow image conversion, Wails v2, vanilla JavaScript, CSS, Node.js test runner, Go.

---

## File Structure

- `generated-images/ainexus-icon-hub-radial.png`: approved source artwork.
- `cmd/desktop/build/appicon.png`: normalized 512px packaged and non-Windows tray icon.
- `cmd/desktop/build/windows/icon.ico`: Windows multi-resolution icon.
- `cmd/desktop/frontend/public/ainexus-icon.png`: 128px desktop header asset copied into Vite output.
- `cmd/desktop/frontend/src/modules/ui.js`: replace the rocket emoji with a semantic image element.
- `cmd/desktop/frontend/src/style.css`: define stable header icon dimensions and alignment.
- `cmd/desktop/frontend/test/header-brand-icon.test.js`: verify the header uses the image asset and no longer renders the rocket.

### Task 1: Lock The Header Contract With A Failing Test

**Files:**
- Create: `cmd/desktop/frontend/test/header-brand-icon.test.js`
- Test: `cmd/desktop/frontend/test/header-brand-icon.test.js`

- [ ] **Step 1: Write the failing test**

```javascript
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const testDir = dirname(fileURLToPath(import.meta.url));
const uiSource = readFileSync(resolve(testDir, '../src/modules/ui.js'), 'utf8');
const cssSource = readFileSync(resolve(testDir, '../src/style.css'), 'utf8');

test('header uses the AINexus brand icon instead of the rocket emoji', () => {
    assert.match(uiSource, /class="app-brand-icon"/);
    assert.match(uiSource, /src="\/ainexus-icon\.png"/);
    assert.doesNotMatch(uiSource, /<h1>🚀/);
});

test('header brand icon has stable dimensions', () => {
    assert.match(cssSource, /\.app-brand-icon\s*\{/);
    assert.match(cssSource, /width:\s*44px/);
    assert.match(cssSource, /height:\s*44px/);
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
cd cmd/desktop/frontend
node --test test/header-brand-icon.test.js
```

Expected: FAIL because `app-brand-icon` and `/ainexus-icon.png` are not present.

- [ ] **Step 3: Commit the failing test**

```bash
git add cmd/desktop/frontend/test/header-brand-icon.test.js
git commit -m "test: define AINexus header icon contract"
```

### Task 2: Generate Packaged Icon Assets

**Files:**
- Modify: `cmd/desktop/build/appicon.png`
- Modify: `cmd/desktop/build/windows/icon.ico`
- Create: `cmd/desktop/frontend/public/ainexus-icon.png`

- [ ] **Step 1: Convert the approved source into production assets**

Run:

```bash
python3 - <<'PY'
from pathlib import Path
from PIL import Image

root = Path.cwd()
source = Image.open(root / "generated-images/ainexus-icon-hub-radial.png").convert("RGBA")
source = source.resize((1024, 1024), Image.Resampling.LANCZOS)

appicon = source.resize((512, 512), Image.Resampling.LANCZOS)
appicon.save(root / "cmd/desktop/build/appicon.png", optimize=True)

header = source.resize((128, 128), Image.Resampling.LANCZOS)
header.save(root / "cmd/desktop/frontend/public/ainexus-icon.png", optimize=True)

source.save(
    root / "cmd/desktop/build/windows/icon.ico",
    format="ICO",
    sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)],
)
PY
```

- [ ] **Step 2: Verify generated formats and dimensions**

Run:

```bash
file cmd/desktop/build/appicon.png \
  cmd/desktop/build/windows/icon.ico \
  cmd/desktop/frontend/public/ainexus-icon.png
sips -g pixelWidth -g pixelHeight \
  cmd/desktop/build/appicon.png \
  cmd/desktop/frontend/public/ainexus-icon.png
```

Expected: PNG dimensions are 512x512 and 128x128; ICO is recognized as a Windows icon resource.

- [ ] **Step 3: Commit generated assets**

```bash
git add cmd/desktop/build/appicon.png \
  cmd/desktop/build/windows/icon.ico \
  cmd/desktop/frontend/public/ainexus-icon.png
git commit -m "assets: replace AINexus desktop icon"
```

### Task 3: Replace The Header Rocket

**Files:**
- Modify: `cmd/desktop/frontend/src/modules/ui.js`
- Modify: `cmd/desktop/frontend/src/style.css`
- Test: `cmd/desktop/frontend/test/header-brand-icon.test.js`

- [ ] **Step 1: Add the image to the header template**

Replace the current title markup with:

```html
<div class="app-brand">
    <img class="app-brand-icon" src="/ainexus-icon.png" alt="" aria-hidden="true">
    <div class="app-brand-copy">
        <h1>${t('app.title')}<span id="broadcast-banner" class="broadcast-banner hidden"></span></h1>
        <p>${t('header.title')}<span id="festivalToggle" class="festival-toggle hidden" onclick="window.toggleFestivalEffect(); event.stopPropagation();" title="${t('festival.toggle') || '切换氛围效果'}"><span class="festival-toggle-name" id="festivalToggleName"></span><span class="festival-toggle-switch" id="festivalToggleSwitch"></span></span></p>
    </div>
</div>
```

- [ ] **Step 2: Add stable icon layout styles**

Add:

```css
.app-brand {
    display: flex;
    align-items: center;
    gap: 12px;
    min-width: 0;
}

.app-brand-icon {
    width: 44px;
    height: 44px;
    flex: 0 0 44px;
    border-radius: 10px;
    object-fit: cover;
}

.app-brand-copy {
    min-width: 0;
}
```

- [ ] **Step 3: Run the focused test**

Run:

```bash
cd cmd/desktop/frontend
node --test test/header-brand-icon.test.js
```

Expected: PASS.

- [ ] **Step 4: Run all frontend tests**

Run:

```bash
cd cmd/desktop/frontend
node --test test/*.test.js
```

Expected: all tests pass.

- [ ] **Step 5: Commit the UI change**

```bash
git add cmd/desktop/frontend/src/modules/ui.js \
  cmd/desktop/frontend/src/style.css
git commit -m "feat: show AINexus icon in desktop header"
```

### Task 4: Build And Visually Verify

**Files:**
- Generated: `cmd/desktop/frontend/dist/ainexus-icon.png`

- [ ] **Step 1: Build the frontend**

Run:

```bash
cd cmd/desktop/frontend
npm run build
```

Expected: Vite build succeeds and `dist/ainexus-icon.png` exists.

- [ ] **Step 2: Compile the desktop package**

Run:

```bash
go test ./cmd/desktop -count=1
```

Expected: package tests pass.

- [ ] **Step 3: Run repository tests affected by embedded assets**

Run:

```bash
go test ./... -count=1
```

Expected: all tests pass, or any unrelated pre-existing failure is documented.

- [ ] **Step 4: Start the desktop development server**

Run:

```bash
cd cmd/desktop
wails dev
```

Expected: the desktop app starts and the top-left header displays the new Radial Hub image.

- [ ] **Step 5: Verify the visual result**

Check:

- the icon is visible beside `AINexus`;
- title and subtitle alignment remains stable;
- no text overlaps at the 800x600 minimum window size;
- the icon remains readable on representative light and dark themes;
- the 32px icon still shows the center and multiple routing channels.

- [ ] **Step 6: Commit any verification-only frontend output required by the repository**

Only stage generated frontend output if it is already tracked by the repository:

```bash
git status --short cmd/desktop/frontend/dist
```

Do not stage unrelated user changes.
