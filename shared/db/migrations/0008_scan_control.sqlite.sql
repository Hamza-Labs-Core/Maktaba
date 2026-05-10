-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0008.
--
-- SQLite 3.35+ supports ``ADD COLUMN IF NOT EXISTS``. Substitutions
-- match slot 0006's parity sibling: BOOLEAN → INTEGER (0/1),
-- REAL → REAL, TEXT → TEXT, TIMESTAMPTZ → TEXT (ISO-8601). The
-- ``CREATE INDEX`` here drops ``CONCURRENTLY`` (SQLite does not
-- support it) but keeps ``IF NOT EXISTS`` so re-applies are safe.
--
ALTER TABLE library_scan_state
    ADD COLUMN IF NOT EXISTS cancel_requested INTEGER NOT NULL DEFAULT 0;
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
    ADD COLUMN IF NOT EXISTS deleted_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS libraries_alive_idx
    ON libraries (id) WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS libraries_alive_idx;
-- +goose StatementEnd
-- SQLite ``DROP COLUMN`` requires 3.35+; the migration tooling for
-- the SQLite path treats this as a no-op rollback (the columns become
-- vestigial on older SQLite, harmless because the `Up` direction is
-- idempotent).
