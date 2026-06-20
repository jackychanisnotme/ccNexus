# Endpoint Rename Data Migration Design

## Problem

Endpoint names currently act as both display labels and persistence keys. In the
desktop flow, changing a name causes configuration persistence to insert the new
endpoint and delete the old endpoint. `DeleteEndpoint` also deletes token-pool
credentials and runtime state, so renaming a Codex Token Pool endpoint empties
its pool.

The server Web API has a related defect: it replaces the in-memory name before
calling an update statement whose `WHERE` clause uses that new name, so a real
rename is not persisted reliably.

## Required Behavior

- Renaming an endpoint preserves its endpoint record and all associated data.
- The behavior applies to desktop and server Web API update paths.
- An active endpoint may not be renamed to another active endpoint's name.
- Historical statistics already stored under the destination name are merged.
- A failed migration leaves all endpoint and associated data unchanged.
- Deleting an endpoint keeps its current destructive cleanup behavior.

## Approach

Add an explicit storage operation:

```go
RenameEndpoint(oldName string, endpoint *Endpoint) error
```

The operation runs in one SQLite transaction. Callers use it only when the
endpoint name changes; ordinary field updates continue to use `UpdateEndpoint`.
This avoids trying to infer renames from a configuration diff, which becomes
ambiguous when endpoints are added, removed, or reordered together.

The desktop endpoint service performs the rename before the general
configuration save. Once the database contains the new name, the existing save
pass sees it as an update and does not delete the old pool. The server Web API
uses the same operation directly.

## Transaction Flow

1. Verify `oldName` exists.
2. Verify the destination name is not used by an active endpoint record.
3. Merge historical `daily_stats` rows from `oldName` into `newName`, grouped by
   `date`, `device_id`, and `client_ip`, summing requests, errors, input tokens,
   and output tokens.
4. Remove the source historical rows after their values have been merged.
5. Update name references in:
   - `endpoint_credentials`
   - `credential_usage`
   - `endpoint_runtime_status`
6. Update the endpoint row using `oldName` in the `WHERE` clause and persist all
   edited endpoint fields.
7. Commit only if every step succeeds; otherwise roll back.

`credential_rate_limits` references credential IDs, so it remains attached
without a direct name update.

If runtime status or credential usage already exists under the destination
name, the migration merges counters where applicable and keeps the most recent
state rather than violating a unique key. This mainly protects databases that
contain stale data from an older endpoint with that name.

## API And Configuration Changes

- Extend the storage interfaces and config adapter with `RenameEndpoint`.
- Preserve the original name in both endpoint update handlers.
- Reject renaming to an existing active endpoint before modifying runtime
  configuration.
- Keep `SaveToStorage` focused on synchronization; callers explicitly perform
  identity-changing renames.

## Testing

Storage integration tests will prove that a rename:

- preserves Codex Token Pool credentials and their IDs;
- preserves credential rate-limit snapshots;
- migrates credential usage and endpoint runtime status;
- merges destination-name historical statistics;
- updates all endpoint fields;
- rejects an active-name collision without partial changes.

Service and server API tests will verify that each update path calls the rename
behavior and leaves the renamed pool accessible under its new name.

## Scope

This change retains name-based associations and adds a safe explicit rename.
Replacing all name references with endpoint IDs would be a larger schema and API
migration and is intentionally outside this bug fix.
