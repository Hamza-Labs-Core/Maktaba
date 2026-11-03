-- +goose Up
-- +goose StatementBegin
--
-- Slot 0181 (Epic 51, story 51.19) - what the home's licence allows, on each desired row.
--
-- A component can name one licence flag in the catalog (today only cloud.relay, by
-- cloud_relay). The API is the one writer of desired state, so it publishes what the
-- licence allows on that component's row, and every runner reads it with the clock:
--
--   components.licensed        true: the licence grants the flag; false: it does not;
--                              NULL: nothing published (no licence flag, or a database
--                              the API has not written yet): the runners follow today.
--   components.licensed_until  the licence's expires_at + SkewGrace (72 h) while it
--                              grants the flag; NULL when it does not, or when the
--                              licence never expires.
--
-- A runner reads Licensed = licensed AND (licensed_until IS NULL OR now < licensed_until),
-- so a runner whose API is down still stops the component when the grace ends. A false
-- Licensed is phase waiting, reason licence_missing; want is not touched.
--
-- The API rewrites the two columns when the licence changes (an apply, a refresh, a
-- revocation, the license_reload loop). That is not a change of the admin's wish: it
-- moves the revision and sends NOTIFY components_changed (the trigger of slot 0174
-- fires on every update), but it does not move the row's generation, so an admin's
-- generation and the runners' observed_generation stay as they are.
--
-- Additive: two nullable columns, and the generation trigger's function replaced by one
-- that leaves the generation alone when only the two columns change.
--
ALTER TABLE components ADD COLUMN IF NOT EXISTS licensed boolean;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE components ADD COLUMN IF NOT EXISTS licensed_until timestamptz;
-- +goose StatementEnd

-- +goose StatementBegin
-- an update of a desired row is a new generation, unless it changed only what the
-- licence allows
CREATE OR REPLACE FUNCTION components_row_next_generation() RETURNS trigger
LANGUAGE plpgsql AS $f$
BEGIN
    IF (NEW.licensed IS DISTINCT FROM OLD.licensed OR NEW.licensed_until IS DISTINCT FROM OLD.licensed_until)
       AND NEW.want IS NOT DISTINCT FROM OLD.want
       AND NEW.hold_by IS NOT DISTINCT FROM OLD.hold_by
       AND NEW.hold_at IS NOT DISTINCT FROM OLD.hold_at
       AND NEW.updated_by IS NOT DISTINCT FROM OLD.updated_by THEN
        NEW.generation := OLD.generation;
        NEW.updated_at := OLD.updated_at;
        RETURN NEW;
    END IF;
    NEW.generation := OLD.generation + 1;
    NEW.updated_at := now();
    RETURN NEW;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER components_generation_trg
    BEFORE UPDATE ON components
    FOR EACH ROW EXECUTE FUNCTION components_row_next_generation();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE TRIGGER components_generation_trg
    BEFORE UPDATE ON components
    FOR EACH ROW EXECUTE FUNCTION components_next_generation();
DROP FUNCTION IF EXISTS components_row_next_generation();
ALTER TABLE components DROP COLUMN IF EXISTS licensed_until;
ALTER TABLE components DROP COLUMN IF EXISTS licensed;
-- +goose StatementEnd
