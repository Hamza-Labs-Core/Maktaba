-- +goose Up
-- +goose StatementBegin
--
-- Slot 0004 (Story 1.6 / plan-01-06) — video state machine.
--
-- Locks the canonical 12-state set onto `videos.state` and (re-)asserts
-- the canonical 7-stage set on `processing_jobs.stage`. Adds a
-- `pg_notify('videos.state_changed', …)` trigger that fires after every
-- state transition so the API/WebSocket layer can stream real-time
-- updates without polling.
--
-- Scope of THIS migration:
--   - Rewrite legacy `processing_jobs.stage = 'thumb'` rows to
--     `'thumbnail'` (resolves REVIEW §1.3.c). Slot 0002's CHECK already
--     rejects 'thumb' going forward; this UPDATE protects against any
--     pre-0002 dump that might be replayed.
--   - Add CHECK constraint `videos_state_valid` enumerating all 12
--     valid states from shared/states/states.json.
--   - Re-assert (idempotently) the stage CHECK as
--     `processing_jobs_stage_valid`; coexists with slot 0002's
--     `processing_jobs_stage_chk` because both enforce the same set
--     and dropping the older one would alter slot 0002's down path.
--   - Add `videos_state_change_notify` AFTER UPDATE trigger: fires
--     `pg_notify('videos.state_changed', json{video_id, library_id,
--     old_state, new_state, updated_at})` whenever `state` actually
--     changes (NEW.state IS DISTINCT FROM OLD.state).
--
-- Out of scope (other slots own these):
--   - The actual transition validity (which from/to pairs are legal) —
--     enforced in application code (Go shared/states + Python
--     domain.states); see plan-01-06 §6.4 for why a SQL trigger cannot
--     do that job.
--   - The state machine's `advance_after_stage` mutator and lint rules
--     that quarantine direct UPDATE statements.
--   - The `videos.new` insert NOTIFY (slot 0005 / plan-01-01).
--
-- Idempotency: every constraint and trigger uses DROP IF EXISTS / ADD
-- so re-running the migration is a no-op. The `'thumb' → 'thumbnail'`
-- UPDATE is naturally idempotent (a second run rewrites zero rows).
--
-- Lock note: ADD CONSTRAINT … NOT VALID + VALIDATE CONSTRAINT keeps
-- the write window short on the hot processing_jobs table; for the
-- smaller videos table a plain ADD CONSTRAINT is fine.
--
UPDATE processing_jobs SET stage = 'thumbnail' WHERE stage = 'thumb';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos
    DROP CONSTRAINT IF EXISTS videos_state_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos
    ADD CONSTRAINT videos_state_valid CHECK (state IN (
        'discovered',
        'probed',
        'audio_extracted',
        'transcribed',
        'indexed',
        'thumbnailed',
        'ready',
        'ready_no_audio',
        'missing',
        'superseded',
        'corrupted',
        'failed'
    ));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs
    DROP CONSTRAINT IF EXISTS processing_jobs_stage_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs
    ADD CONSTRAINT processing_jobs_stage_valid CHECK (stage IN (
        'scan',
        'probe',
        'extract',
        'transcribe',
        'subtitle_gen',
        'index',
        'thumbnail'
    )) NOT VALID;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs
    VALIDATE CONSTRAINT processing_jobs_stage_valid;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION videos_state_change_notify() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    -- Fire only on real transitions; spurious UPDATEs that re-write the
    -- same value (e.g. updated_at-only writes) are filtered. The IS
    -- DISTINCT FROM operator handles NULLs correctly even though the
    -- column is NOT NULL — defence in depth against future schema work.
    IF NEW.state IS DISTINCT FROM OLD.state THEN
        PERFORM pg_notify(
            'videos.state_changed',
            json_build_object(
                'video_id',   NEW.id,
                'library_id', NEW.library_id,
                'old_state',  OLD.state,
                'new_state',  NEW.state,
                'updated_at', NEW.updated_at
            )::text
        );
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS videos_state_change_notify_trg ON videos;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER videos_state_change_notify_trg
    AFTER UPDATE OF state ON videos
    FOR EACH ROW
    EXECUTE FUNCTION videos_state_change_notify();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS videos_state_change_notify_trg ON videos;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS videos_state_change_notify();
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs
    DROP CONSTRAINT IF EXISTS processing_jobs_stage_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos
    DROP CONSTRAINT IF EXISTS videos_state_valid;
-- +goose StatementEnd
