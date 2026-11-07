-- +goose Up
-- +goose StatementBegin
--
-- Slot 0197 - Maktaba's own stages as first-party plugins (Epic 54, stories 54.22-54.27).
--
-- job_stages.impl     where a stage runs: 'inproc' (its code in the pipeline worker, as
--                     before) or 'plugin' (the same code in maktaba-plugin-runner, which
--                     reaches the database only through the plugin API by its named
--                     statements). The worker reads it for each job (story 54.25 §3); a
--                     machine whose runner does not serve the stage runs it in-process.
-- job_stages.flipping a flip under way (PUT /api/queues/{stage}/impl): no new claims of
--                     the stage until it is set.
-- job_stages.impl_changed_at / impl_changed_by  the last flip and who made it (audited).
--
-- Every stage starts 'inproc': nothing changes until a wave's migration flips its stages.
-- The values are checked by a trigger, not a constraint (a release rolled back to the one
-- before keeps writing rows that pass; ADD CONSTRAINT would make that rollback unsafe).
--
-- Additive: an older release ignores the columns and runs every stage in-process, which
-- writes the same data (the parity proof, story 54.26).
--
ALTER TABLE job_stages ADD COLUMN IF NOT EXISTS impl TEXT NOT NULL DEFAULT 'inproc';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE job_stages ADD COLUMN IF NOT EXISTS flipping BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE job_stages ADD COLUMN IF NOT EXISTS impl_changed_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE job_stages ADD COLUMN IF NOT EXISTS impl_changed_by TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION job_stages_impl_check() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.impl IS NULL OR NEW.impl NOT IN ('inproc', 'plugin') THEN
        RAISE EXCEPTION 'job_stages.impl must be inproc or plugin, not %', NEW.impl
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER job_stages_impl_check_trg
    BEFORE INSERT OR UPDATE OF impl ON job_stages
    FOR EACH ROW EXECUTE FUNCTION job_stages_impl_check();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS job_stages_impl_check_trg ON job_stages;
DROP FUNCTION IF EXISTS job_stages_impl_check();
ALTER TABLE job_stages DROP COLUMN IF EXISTS impl_changed_by;
ALTER TABLE job_stages DROP COLUMN IF EXISTS impl_changed_at;
ALTER TABLE job_stages DROP COLUMN IF EXISTS flipping;
ALTER TABLE job_stages DROP COLUMN IF EXISTS impl;
-- +goose StatementEnd
