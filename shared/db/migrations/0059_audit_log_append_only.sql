-- +goose Up
-- +goose StatementBegin
--
-- Slot 0059 (gap-closure HLB-359 / HLB-311) — make `audit_log`
-- genuinely append-only.
--
-- Slot 0036 created `audit_log` and slot 0054 extended it additively.
-- Both describe it as an "append-only security log", but nothing at
-- the SQL layer enforced that: any role with table access could
-- silently UPDATE or DELETE rows, defeating the entire point of a
-- tamper-evident audit trail. This slot closes that gap with a
-- row-level guard trigger that raises on every UPDATE and DELETE so
-- the table is INSERT + SELECT only at the database boundary —
-- application code cannot tamper with history even by mistake.
--
-- Scope of THIS migration:
--   - `audit_log_no_mutate()` PL/pgSQL function that unconditionally
--     raises on the row it is fired for.
--   - `audit_log_append_only_trg` BEFORE UPDATE OR DELETE trigger on
--     `audit_log`, FOR EACH ROW. INSERT is deliberately NOT in the
--     trigger event list, so every existing writer
--     (api securityaudit.Write, api libraries.WriteAudit, the pipeline
--     AuditWriter) keeps working unchanged.
--
-- Partitioning (the should-have) is DEFERRED, not shipped here. See the
-- block comment below the trigger DDL for the rationale; it is also
-- reported as a follow-up concern in the slot's PR.
--
-- Idempotency: the function uses idempotent-create (replace-in-place)
-- and the trigger is dropped-if-exists then re-created, matching the
-- slot-0005 `videos_notify_new` convention so re-application is a
-- no-op.
--
CREATE OR REPLACE FUNCTION audit_log_no_mutate() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION
        'audit_log is append-only: % is not permitted (row id=%)',
        TG_OP, COALESCE(OLD.id, NEW.id)
        USING ERRCODE = 'restrict_violation',
              HINT = 'Insert a compensating row instead of mutating history.';
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS audit_log_append_only_trg ON audit_log;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER audit_log_append_only_trg
    BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW
    EXECUTE FUNCTION audit_log_no_mutate();
-- +goose StatementEnd

-- Partitioning DEFERRED — rationale (HLB-311).
--
-- Range-partitioning audit_log by month on the timestamp column is the
-- should-have for this gap, but converting the existing, populated
-- relation into a partitioned one is a data-moving migration in this
-- schema and is intentionally not shipped here:
--
--   1. audit_log.id is a single-column BIGSERIAL primary key (slot
--      0036). Postgres requires the partition key to be a member of
--      every unique / primary-key constraint, so partitioning by ts
--      forces the key to become (id, ts). A plain relation cannot be
--      turned into a partitioned one in place: it needs a fresh
--      partitioned parent, a copy of every existing row, then an
--      atomic rename — exactly the rewrite migrations/README.md §4
--      forbids in a single slot, and the kind of risky data-moving
--      change Wave 1 was told not to ship blind.
--   2. audit_log is referenced by read paths (api securityaudit
--      ListRecent, api libraries audit list) and three write paths;
--      a rename/copy must be choreographed as ship-DDL then
--      backfill-job then flip-read-path (README §4/§5), its own slot.
--
-- The security-critical guarantee (append-only / tamper-evidence) is
-- delivered unconditionally by the trigger above and does not depend
-- on partitioning. Partitioning is a retention/performance
-- optimisation tracked as a separate follow-up slot using the
-- standard partitioned-shadow then backfill-job then atomic-swap
-- pattern. This comment is the deliberate, documented deferral the
-- Wave 1 brief requires.

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS audit_log_append_only_trg ON audit_log;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS audit_log_no_mutate();
-- +goose StatementEnd
