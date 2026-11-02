-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0172: orphaned stage pauses (paused with no paused_by)
-- are resumed with an audit_log row each, a pause that names its owner but no time gets
-- updated_at as its time; triggers refuse a pause without paused_at and paused_by;
-- claim_shares holds each stage's share of its pool's claims and the pool's clock; and
-- processing_jobs_claimable_idx makes each stage's head one index probe.
--
INSERT INTO audit_log (category, action, actor_source, target_kind, target_id, payload)
SELECT 'admin', 'queue.stage_orphan_pause_resumed', 'migration', 'job_stage', stage,
       json_object('reason', 'stage was paused with no paused_by: nothing could release it',
                   'paused_at', paused_at,
                   'updated_at', updated_at,
                   'migration', '0172')
  FROM job_stages
 WHERE paused <> 0 AND paused_by IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages
   SET paused = 0, paused_at = NULL, paused_by = NULL,
       updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
 WHERE paused <> 0 AND paused_by IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET paused_at = updated_at WHERE paused <> 0 AND paused_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS job_stages_pause_owner_ins
    BEFORE INSERT ON job_stages
    FOR EACH ROW
    WHEN NEW.paused <> 0 AND (NEW.paused_at IS NULL OR NEW.paused_by IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'job_stages: stage paused without paused_at and paused_by');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS job_stages_pause_owner_upd
    BEFORE UPDATE ON job_stages
    FOR EACH ROW
    WHEN NEW.paused <> 0 AND (NEW.paused_at IS NULL OR NEW.paused_by IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'job_stages: stage paused without paused_at and paused_by');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS claim_shares (
    owner TEXT    PRIMARY KEY,
    pass  INTEGER NOT NULL DEFAULT 0
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_claimable_idx
    ON processing_jobs (stage, priority, id)
    WHERE state IN ('pending', 'paused');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS processing_jobs_claimable_idx;
DROP TABLE IF EXISTS claim_shares;
DROP TRIGGER IF EXISTS job_stages_pause_owner_upd;
DROP TRIGGER IF EXISTS job_stages_pause_owner_ins;
-- +goose StatementEnd
