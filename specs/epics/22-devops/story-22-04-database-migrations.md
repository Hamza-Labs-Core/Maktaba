# Story 22.4 — Database migrations

Schema is owned by `goose` migrations under `shared/db/migrations/`.
Migrations are forward-only at the bytes level; rollback is a manual,
documented operation.

## Acceptance criteria

- AC1. Migrations are append-only: edits to a previously-merged
  migration are forbidden by a CI lint comparing against the main
  branch.
- AC2. `maktaba-api migrate up` runs at boot by default behind a
  feature flag; a separate `--migrate-only` flag runs migrations and
  exits, used in deployments where boot-time migration is undesirable.
- AC3. Every migration has an idempotency guard (`IF NOT EXISTS`,
  `IF EXISTS`) so re-running is a no-op.
- AC4. Long-running DDL is forbidden in v1: a CI lint flags
  unsupported patterns (`CREATE INDEX` without `CONCURRENTLY` on
  Postgres, table rewrites without batched migration plan).
- AC5. SQLite parity: each migration ships a `.sqlite.sql` variant
  reviewed and tested.

## Test cases

- TC1. Append-only: editing the SQL of a merged migration fails CI.
- TC2. Idempotent: run `migrate up` twice; second run is a no-op
  (no DDL emitted, no errors).
- TC3. Long-running guard: a deliberate `CREATE INDEX` (without
  CONCURRENTLY) on a > 10 k row table fails the lint with a fix-it
  hint.

## Edge cases

- EC1. Down migrations exist in the repo but are unsupported in
  production paths; documented as "for local dev only."
- EC2. Migration that requires a backfill — pattern: ship the DDL,
  run a separate idempotent backfill job tracked in `processing_jobs`,
  flip the read path. The pattern is documented and tested.
- EC3. SQLite missing a Postgres-only feature (e.g., partial indexes)
  — the SQLite variant uses a fallback (full index + filter); parity
  test asserts query results are identical.
