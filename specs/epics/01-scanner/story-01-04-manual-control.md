# Story 1.4 — Manual control surface

## Description

Operators need to scan on demand, check progress, and stop a runaway scan.
This story owns the imperative control surface (the API contract for
scan trigger and cancellation) and the CLI dry-run.

## Acceptance criteria

- **Given** a running scan,
  **when** the user calls `POST /api/libraries/{id}/scan` again,
  **then** the request returns 200 with `{status: "already_running",
  progress: <pct>}`; a second scan is not started.
- **Given** a long-running scan,
  **when** the user calls `DELETE /api/libraries/{id}/scan`,
  **then** the scanner stops within 5 s after the next file boundary,
  rolls back any uncommitted batch, and the library state is consistent
  (no orphaned `processing_jobs`).
- **Given** the CLI invocation
  `maktaba-pipeline scan --library NAME --dry-run`,
  **when** it runs,
  **then** it prints the would-be inserts to stdout and writes nothing
  to the DB.

## Test cases

- `test_scan_idempotent_concurrent_invocation` — invoke scan twice in
  parallel; the second call returns `already_running` and no duplicate
  rows are produced.
- `test_scan_cancellation_cleans_up` — cancel mid-scan → no
  `processing_jobs` rows reference videos that don't exist, no half-
  inserted videos.
- `test_dry_run_writes_nothing` — fixture tree, `--dry-run` → DB row
  counts are unchanged.

## Edge cases

- **CLI invocation while the gRPC server is also running.** The CLI
  acquires the same per-library scan advisory lock the gRPC trigger uses
  (Postgres `pg_try_advisory_lock(hashtext('scan:' || library_id))`);
  one of the two backs off.
- **Library deleted mid-scan.** The scan exits cleanly the next time it
  checks `library.deleted_at IS NOT NULL`.
