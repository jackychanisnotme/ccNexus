# Endpoint Status Badge & List Layout Redesign

Date: 2025-06-14
Scope: cmd/desktop/frontend only

## Design Read

Reading this as: desktop utility tool for developers, with an Apple-y / calm system tool language, leaning toward native CSS + restrained toned-down badge tints.

## Design Decisions

### Badge colors → low-saturation tint approach

1. inUse (runtime-badge-active)
   Replace #0ea5e9 with #4a9bd9 (muted steel blue), bg rgba(74,155,217,0.12), thin border.

2. recentSuccess (runtime-badge-success)
   Replace #16a34a with #4a9d6e (muted sage green), bg rgba(74,157,110,0.12).

3. recentFailure (runtime-badge-failure)
   Replace #dc2626 with #d95b5b (muted rose red), bg rgba(217,91,91,0.12).

All badges get 1px solid border tinted to the same hue and consistent sizing.

### Failure display priority

- Badge shows HTTP {code} (e.g. HTTP 503) when status code known, followed by time.
- Full English reason+statusCode moves into title tooltip.
- Compact view uses labelPrefix of HTTP {code} or Err for non-HTTP reasons.

### Status slot stability

- .endpoint-status-badges and .compact-runtime-slot get fixed min-width and overflow clip.
- Individual .runtime-badge uses overflow:hidden; text-overflow:ellipsis; full label in title.
- Detail view h3 badge row uses display:flex; flex-wrap:nowrap; min-width:0.

### Compact list overflow fix

- .endpoint-item-compact becomes display:grid with fixed column templates respecting min-width:0.
- .compact-url, .compact-runtime-slot, .compact-stats enforce overflow:hidden + text-overflow:ellipsis.
- .endpoint-list.compact-view stays within container, no horizontal scroll bleed.

## Files touched

- cmd/desktop/frontend/src/modules/endpoints.js
- cmd/desktop/frontend/src/style.css
- cmd/desktop/frontend/src/simple-view.css
