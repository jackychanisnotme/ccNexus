# License Admin Session Design

## Goal

Optimize the license server admin UI at `/admin/` by replacing browser Basic Auth with a real logout-capable session, adding card history, adding card deletion, and protecting sensitive endpoints with authorization and rate limiting.

## Scope

- Keep the license server as the independent `cmd/license-server` binary.
- Keep the admin UI as server-rendered HTML embedded in `cmd/license-server/main.go`.
- Keep license domain logic inside `internal/onlinelicense`.
- Do not introduce a JavaScript build step or external frontend dependency.

## Authentication

The admin page uses form login instead of Basic Auth. `POST /api/admin/login` validates the configured admin username and password, creates a random server-side session, and sets an `HttpOnly` `SameSite=Lax` cookie. `POST /api/admin/logout` deletes that session and clears the cookie. Admin HTML and `/api/admin/*` endpoints require a valid session, except login endpoints.

Sessions are in memory and expire after 12 hours. Restarting the license server logs admins out, which is acceptable for this small operational service.

## Rate Limiting

The HTTP handler applies in-memory per-IP rate limits:

- Login attempts: strict limit to reduce password guessing.
- Public license activation and refresh: moderate limit to reduce abuse.
- Admin mutation endpoints such as generate, delete, disable, and logout: moderate limit to reduce accidental repeat submissions.

Limit failures return `429` JSON errors.

## Card History

The existing `admin_audit_logs` table becomes the history source. The store exposes a list method and the UI renders the newest events. The service records generate, activate, refresh, disable card, disable activation, delete card, login, and logout events where relevant.

## Card Deletion

Deleting a card is a deliberate hard delete from `license_cards` and related `license_activations`, while preserving the audit log entry for the deletion. The UI confirms before deletion.

## Admin UI

The admin UI remains dense and operational. It gets:

- A top-right logout button.
- A card table action for disable and delete.
- A history table below activations.
- Clear login, unauthorized, and rate-limit error handling.

## Testing

Tests cover session login/logout, admin endpoint authorization, rate limits, card deletion, and history listing. Existing Basic Auth behavior is replaced for the license server admin API.
