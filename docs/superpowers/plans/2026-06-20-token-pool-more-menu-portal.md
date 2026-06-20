# Token Pool More Menu Portal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep every Codex Token Pool account action menu fully visible, including menus opened from the bottom rows of the credential table.

**Architecture:** Track the currently open account menu, move it from its table cell to `document.body`, and position it against the trigger button with fixed viewport coordinates. A single close helper restores the menu and clears inline portal styles on outside click, scrolling, resizing, modal close, refresh, or action selection.

**Tech Stack:** Vanilla JavaScript, CSS, Node.js built-in test runner, Vite

---

### Task 1: Specify portal behavior

**Files:**
- Modify: `cmd/desktop/frontend/test/endpoint-modal-layout.test.js`

- [ ] **Step 1: Write the failing source-contract test**

Add a test that requires portal state, body mounting, fixed-position CSS, viewport measurement, and cleanup listeners:

```js
it('portals token pool action menus outside the scrolling table', () => {
    assert.match(endpointsSource, /let tokenPoolOpenActionMenu = null;/);
    assert.match(endpointsSource, /document\.body\.appendChild\(menu\)/);
    assert.match(endpointsSource, /button\.getBoundingClientRect\(\)/);
    assert.match(endpointsSource, /closeAllTokenPoolActionMenus\(\)/);
    assert.match(endpointsSource, /addEventListener\('scroll',[\s\S]*true\)/);
    assert.match(endpointsSource, /addEventListener\('resize'/);
    assert.match(cssSource, /\.token-pool-more-menu\.token-pool-more-menu-portal\s*{[^}]*position:\s*fixed;/s);
}
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run: `node --test cmd/desktop/frontend/test/endpoint-modal-layout.test.js`

Expected: FAIL because the portal state and body mounting do not exist yet.

- [ ] **Step 3: Commit the failing test**

```bash
git add cmd/desktop/frontend/test/endpoint-modal-layout.test.js
git commit -m "test: cover token pool action menu portal"
```

### Task 2: Implement and verify the menu portal

**Files:**
- Modify: `cmd/desktop/frontend/src/modules/endpoints.js`
- Modify: `cmd/desktop/frontend/src/style.css`
- Test: `cmd/desktop/frontend/test/endpoint-modal-layout.test.js`

- [ ] **Step 1: Add portal state and lifecycle helpers**

Add module state and helpers that close the previous menu, restore it to its original wrapper, clear its portal class and inline coordinates, then open a selected menu by appending it to `document.body`. Measure the trigger and menu with `getBoundingClientRect()`, prefer below placement, fall back above, and clamp `left` within an 8px viewport margin.

```js
let tokenPoolOpenActionMenu = null;

function closeAllTokenPoolActionMenus() {
    if (!tokenPoolOpenActionMenu) return;
    const { menu, wrap } = tokenPoolOpenActionMenu;
    menu.classList.remove('show', 'token-pool-more-menu-portal');
    menu.style.left = '';
    menu.style.top = '';
    if (wrap?.isConnected) wrap.appendChild(menu);
    else menu.remove();
    tokenPoolOpenActionMenu = null;
}

function openTokenPoolActionMenu(button, menu, wrap) {
    closeAllTokenPoolActionMenus();
    document.body.appendChild(menu);
    menu.classList.add('show', 'token-pool-more-menu-portal');
    const triggerRect = button.getBoundingClientRect();
    const menuRect = menu.getBoundingClientRect();
    const margin = 8;
    const left = Math.min(
        Math.max(margin, triggerRect.right - menuRect.width),
        window.innerWidth - menuRect.width - margin,
    );
    const below = triggerRect.bottom + 4;
    const top = below + menuRect.height <= window.innerHeight - margin
        ? below
        : Math.max(margin, triggerRect.top - menuRect.height - 4);
    menu.style.left = `${left}px`;
    menu.style.top = `${top}px`;
    tokenPoolOpenActionMenu = { menu, wrap };
}
```

- [ ] **Step 2: Route open and close events through the helpers**

Close before replacing credential rows and before closing the modal. Update the trigger handler to call `openTokenPoolActionMenu(button, menu, wrap)`. Register module-level document click, scroll capture, and resize listeners once, and keep action selection routed through `closeAllTokenPoolActionMenus()`.

```js
document.addEventListener('click', closeAllTokenPoolActionMenus);
window.addEventListener('scroll', closeAllTokenPoolActionMenus, true);
window.addEventListener('resize', closeAllTokenPoolActionMenus);
```

- [ ] **Step 3: Add portal-specific CSS**

```css
.token-pool-more-menu.token-pool-more-menu-portal {
    position: fixed;
    right: auto;
    z-index: 10000;
}
```

- [ ] **Step 4: Run the focused test**

Run: `node --test cmd/desktop/frontend/test/endpoint-modal-layout.test.js`

Expected: all tests PASS.

- [ ] **Step 5: Build the frontend**

Run: `npm run build --prefix cmd/desktop/frontend`

Expected: Vite completes successfully with exit code 0.

- [ ] **Step 6: Manually verify the bottom-row menu**

Open the desktop frontend, enter Codex Token Pool account management, scroll the credential table to the last row, and click "More". Confirm the full menu is visible above or below the trigger and closes on table scroll, window resize, outside click, and modal close.

- [ ] **Step 7: Commit the implementation**

```bash
git add cmd/desktop/frontend/src/modules/endpoints.js cmd/desktop/frontend/src/style.css
git commit -m "fix: keep token pool action menu visible"
```
