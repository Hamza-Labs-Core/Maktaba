-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0059 (gap-closure HLB-359 / HLB-311).
--
-- Unlike LISTEN/NOTIFY (slot 0005), SQLite *does* have a faithful
-- equivalent for the append-only guard: BEFORE UPDATE / BEFORE DELETE
-- triggers that call RAISE(ABORT, …) abort the offending statement, so
-- the dev/test SQLite build enforces the same tamper-evidence as
-- Postgres. INSERT is not guarded, so every writer keeps working.
--
-- SQLite uses CREATE TRIGGER IF NOT EXISTS for idempotency (same
-- pattern as slot 0016's FTS sync triggers); there is no
-- CREATE OR REPLACE TRIGGER in SQLite.
--
CREATE TRIGGER IF NOT EXISTS audit_log_no_update_trg
    BEFORE UPDATE ON audit_log
    FOR EACH ROW
    BEGIN
        SELECT RAISE(ABORT, 'audit_log is append-only: UPDATE is not permitted');
    END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS audit_log_no_delete_trg
    BEFORE DELETE ON audit_log
    FOR EACH ROW
    BEGIN
        SELECT RAISE(ABORT, 'audit_log is append-only: DELETE is not permitted');
    END;
-- +goose StatementEnd

-- +goose StatementBegin
--
-- Partitioning is deferred on Postgres (declarative range partitioning
-- on a populated table is a data-moving migration — see the Postgres
-- sibling's rationale). SQLite has no native table partitioning at all,
-- so there is nothing to mirror here; the append-only guarantee above
-- is the security-critical, portable part of this slot.
--
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS audit_log_no_delete_trg;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS audit_log_no_update_trg;
-- +goose StatementEnd
