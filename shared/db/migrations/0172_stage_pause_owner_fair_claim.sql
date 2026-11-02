-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0172 - a stage pause always has an owner, and no stage of a pool starves.
--
-- 1. Orphaned stage pauses are resumed. On 2026-10-22 22:37 (EDT) the extract stage of the
--    home was paused by hand with a bare `UPDATE job_stages SET paused = true` (no
--    paused_at, no paused_by). Nothing in the system could release it: the share-path
--    conversion resumes only its own pauses (paused_by = 'storage_canonicalize') and the
--    Queues page showed only "Paused" with no owner. A stage paused by nobody is
--    resumed here, with an audit_log row per stage (category 'admin', action
--    'queue.stage_orphan_pause_resumed', target_kind 'job_stage'). A pause that names its owner but no time gets the
--    row's updated_at as its time.
--
-- 2. job_stages_pause_owner_trg refuses a pause without paused_at and paused_by, on
--    INSERT and UPDATE. A trigger and not a CHECK constraint: an added constraint makes
--    the release unsafe to roll back by keeping the schema (the updater restores the
--    pre-update dump instead, cmd/maktaba-server/internal/updater/schemakeep.go), and
--    the release before already writes both columns on every pause (the admin pause in
--    api/internal/handlers/queues/control.go, the share-path conversion in
--    pipeline/src/maktaba_pipeline/sharepath/auto.py). The SQLite twin has the same
--    triggers.
--
-- 3. claim_shares: the fair share of a pool's claims among its priority classes
--    (pipeline/src/maktaba_pipeline/engine/claim.py). Row 'prio:<pool>:<priority>' holds
--    when the last claim at that priority was due, row 'pool:<name>' the pool's clock. A
--    claim takes the head (best priority, then oldest) of each stage of the pool, and the
--    head due first: due = GREATEST(class pass + head priority, pool clock); heads of one
--    priority stay in id order. Jobs at priority 900 get one claim for every nine at 100
--    instead of none while those have work. Jobs at priority 50 or better (Process now) are still claimed
--    first, strictly by priority.
--
-- 4. processing_jobs_claimable_idx (stage, priority, id) over the claimable states: each
--    stage's head is one index probe.
--
-- Additive: one table, one index, one trigger; the data change resumes orphaned pauses.
--
DO $$
DECLARE
    v_n integer;
BEGIN
    INSERT INTO audit_log (category, action, actor_source, target_kind, target_id, payload)
    SELECT 'admin', 'queue.stage_orphan_pause_resumed', 'migration', 'job_stage', s.stage,
           jsonb_build_object(
               'reason', 'stage was paused with no paused_by: nothing could release it',
               'paused_at', s.paused_at,
               'updated_at', s.updated_at,
               'migration', '0172')
      FROM job_stages s
     WHERE s.paused AND s.paused_by IS NULL;
    UPDATE job_stages
       SET paused = false, paused_at = NULL, paused_by = NULL, updated_at = now()
     WHERE paused AND paused_by IS NULL;
    GET DIAGNOSTICS v_n = ROW_COUNT;
    IF v_n > 0 THEN
        RAISE NOTICE 'slot 0172: resumed % stage(s) paused by nobody', v_n;
    END IF;
    UPDATE job_stages SET paused_at = updated_at WHERE paused AND paused_at IS NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION job_stages_pause_owner_check() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'job_stages: stage % paused without paused_at and paused_by', NEW.stage
        USING ERRCODE = 'check_violation',
              HINT = 'pause a stage through POST /api/queues/{stage}/pause, or set paused_at and paused_by';
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER job_stages_pause_owner_trg
    BEFORE INSERT OR UPDATE ON job_stages
    FOR EACH ROW
    WHEN (NEW.paused AND (NEW.paused_at IS NULL OR NEW.paused_by IS NULL))
    EXECUTE FUNCTION job_stages_pause_owner_check();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS claim_shares (
    owner TEXT   PRIMARY KEY,
    pass  BIGINT NOT NULL DEFAULT 0
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_claimable_idx
    ON processing_jobs (stage, priority, id)
    WHERE state IN ('pending', 'paused');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS processing_jobs_claimable_idx;
DROP TABLE IF EXISTS claim_shares;
DROP TRIGGER IF EXISTS job_stages_pause_owner_trg ON job_stages;
DROP FUNCTION IF EXISTS job_stages_pause_owner_check();
-- +goose StatementEnd
