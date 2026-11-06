-- +goose Up
-- +goose StatementBegin
--
-- Slot 0190 - only the home asks for a restart, and a restart's seq only goes up.
--
-- component_restarts (slot 0185) kept the default grants of a new table, so maktaba_worker
-- (every joined machine's login role) could write any row, while slot 0178 took writes on
-- the component tables away from it. Both runners restart a component's loops only when
-- its seq is above the one they last read (shared/components/runner, the pipeline's
-- components/runner.py): a row moved down was silently ignored by every later request
-- until it climbed back (#663 review).
--
-- component_restarts_guard() refuses, row by row:
--   * any insert, update or delete by a member of maktaba_worker that is not also a member
--     of the table's owner (the home's own role, and a superuser, are);
--   * an update that lowers seq.
-- The grants stay as slot 0185 left them: the previous release re-makes maktaba_worker
-- with the default grants, and its worker-group check reads a revoke as a change.
--
-- Additive: one function, one trigger.
--
CREATE OR REPLACE FUNCTION component_restarts_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    owner name := (SELECT pg_get_userbyid(c.relowner) FROM pg_class c WHERE c.oid = TG_RELID);
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker')
       AND pg_has_role(current_user, 'maktaba_worker', 'MEMBER')
       AND NOT pg_has_role(current_user, owner, 'MEMBER')
    THEN
        RAISE EXCEPTION 'component_restarts: only the home asks for a restart (role %)', current_user
            USING ERRCODE = '42501';
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.seq < OLD.seq THEN
        RAISE EXCEPTION 'component_restarts: % / % / %: seq % cannot go down to %',
            OLD.machine, OLD.id, OLD.host, OLD.seq, NEW.seq
            USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER component_restarts_guard_trg
    BEFORE INSERT OR UPDATE OR DELETE ON component_restarts
    FOR EACH ROW EXECUTE FUNCTION component_restarts_guard();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- dev only (README rule 6)
DROP TRIGGER IF EXISTS component_restarts_guard_trg ON component_restarts;
DROP FUNCTION IF EXISTS component_restarts_guard();
-- +goose StatementEnd
