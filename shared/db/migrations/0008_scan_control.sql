-- +goose Up
-- +goose NO TRANSACTION
--
-- Slot 0008 (Story 1.4 / plan-01-04) — manual scan control surface.
--
-- Extends slot 0006's ``library_scan_state`` with two control columns
-- so the API and CLI can stop a runaway scan and the scanner can
-- self-report progress without re-deriving it from row counts:
--
--   * ``cancel_requested`` — set to ``true`` by the
--     ``DELETE /api/libraries/{id}/scan`` handler (or
--     ``maktaba-pipeline scan --cancel``); polled by the orchestrator
--     every ``cancel_poll_every`` files.
--   * ``progress_pct`` — written on the same poll round-trip; read by
--     the ``GET /api/libraries/{id}/scan`` projection so the API does
--     not have to compute the percent from raw counters.
--   * ``last_error`` — populated by the orchestrator when a scan exits
--     with an error sentinel (``library_deleted``, ``cancelled``);
--     surfaced verbatim by the GET handler.
--
-- Adds ``libraries.deleted_at`` (idempotent — slot 0007 may add the
-- column on ``videos`` already; libraries needs its own copy for the
-- "library deleted mid-scan" edge case in Story 1.4). The
-- ``libraries_alive_idx`` partial index makes ``WHERE deleted_at IS
-- NULL`` lookups O(1) at any library count.
--
-- The directive on line 1 disables goose's per-migration transaction
-- wrapper because ``CREATE INDEX CONCURRENTLY`` cannot run inside a
-- Postgres transaction. Every statement uses
-- ``IF [NOT] EXISTS`` so a partially-applied migration can be re-run.
--

-- +goose StatementBegin
ALTER TABLE library_scan_state
    ADD COLUMN IF NOT EXISTS cancel_requested BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE library_scan_state
    ADD COLUMN IF NOT EXISTS progress_pct REAL NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE library_scan_state
    ADD COLUMN IF NOT EXISTS last_error TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE libraries
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS libraries_alive_idx
    ON libraries (id) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS libraries_alive_idx;
-- +goose StatementBegin
ALTER TABLE libraries           DROP COLUMN IF EXISTS deleted_at;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE library_scan_state  DROP COLUMN IF EXISTS last_error;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE library_scan_state  DROP COLUMN IF EXISTS progress_pct;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE library_scan_state  DROP COLUMN IF EXISTS cancel_requested;
-- +goose StatementEnd
