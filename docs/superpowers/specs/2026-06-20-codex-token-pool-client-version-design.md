# Codex Token Pool Client Version Compatibility

## Problem

Codex token-pool endpoint tests and production proxy requests identify themselves
as Codex CLI `0.101.0`. The ChatGPT Codex backend rejects `gpt-5.5` requests from
that version with HTTP 400 and asks the client to upgrade.

## Design

Define the stable Codex compatibility identity once and use it for both endpoint
test requests and production proxy requests. Set the identity to Codex CLI
`0.141.0`, the current stable npm release verified on 2026-06-20. Keep the
existing originator, session, account, authorization, and streaming headers
unchanged.

This change must not rewrite endpoint models or silently downgrade `gpt-5.5`.
The upstream should receive the configured model together with a supported
client identity.

## Components

- `internal/config`: owns the shared Codex client version and user-agent values.
- `internal/service`: uses the shared identity for desktop endpoint tests.
- `internal/proxy`: uses the shared identity for real token-pool proxy traffic
  and rate-limit requests.

## Error Handling

Existing upstream status and response-body reporting remains unchanged. A
future upstream compatibility failure will therefore still be visible to the
user instead of being hidden by fallback model substitution.

## Testing

Add focused regression tests proving that endpoint-test requests and production
proxy requests send version `0.141.0` and a matching user agent. Run the affected
package tests, followed by the full Go test suite if the focused tests pass.

## Scope

No changes to token selection, refresh, quarantine, endpoint model selection,
or non-Codex authentication modes.
