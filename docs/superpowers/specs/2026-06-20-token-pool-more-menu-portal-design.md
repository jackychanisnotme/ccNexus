# Codex Token Pool More Menu Portal Design

## Problem

The credential table is inside `.token-pool-table-wrap`, which uses `overflow: auto`. The account action menu is absolutely positioned inside that scroll container, so menus opened from the bottom rows are clipped by the container boundary.

## Design

When a Token Pool account's "More" button opens its menu, move the menu element to `document.body` and position it with `position: fixed` from the button's viewport coordinates. Prefer placing the menu below the button; place it above when the viewport has insufficient space below. Clamp the horizontal position to the viewport so the full menu remains visible.

Keep one menu open at a time. Clicking outside the menu, selecting an action, scrolling the table or window, resizing the window, refreshing the credential list, or closing the modal must close the menu and remove its portal positioning state. The existing action buttons and behavior remain unchanged.

## Implementation Boundaries

- Keep the change within the Token Pool account action menu code and its styles.
- Add small helpers for opening, positioning, and closing the portaled menu.
- Preserve the existing table structure and scrolling behavior.
- Do not change other endpoint "More" menus.

## Verification

- Add frontend tests that assert the menu is moved to `document.body`, uses viewport positioning, and is cleaned up when closed.
- Build the desktop frontend.
- Manually verify a bottom-row menu is fully visible and remains aligned with its button when opened.
