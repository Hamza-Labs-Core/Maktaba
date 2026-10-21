-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0143 (Epic 40, queue health) - a red tile means something is wrong.
--
--   processing_jobs.failure_class   the class of the job's latest failure: transient,
--                                   dependency:<name>, resource:<name>, item_permanent,
--                                   unknown (engine/failure_classes.py)
--   processing_jobs.parked_on       set while a pending job waits on a dependency or a
--                                   resource (no attempt spent); the claim clears it, a
--                                   dependency watcher releases it (not_before = now)
--   dependency_health               the watchers' last probe of each dependency
--   failure_ignores                 an admin's "ignore": the subject's failures of that
--                                   stage leave every count, tile and badge
--   item_requests                   an admin's request on one video (redownload), done by
--                                   a stage (repair_broken)
--   item_checks                     the last access check of a video's file, from the
--                                   worker host that ran it (stage access_check)
--   videos.missing_confirmed_at     a scan found the share up and the file still gone
--   job_stages 'access_check'       reads 64 KiB of a video's file on the worker host
--
-- Additive: no row changes (today's failures are moved by the pipeline, story 40.6).
--
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS failure_class TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS parked_on TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_parked_idx
    ON processing_jobs (parked_on, stage) WHERE parked_on IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS dependency_health (
    name        TEXT        PRIMARY KEY,
    healthy     BOOLEAN     NOT NULL,
    message     TEXT,
    checked_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- when it last turned healthy or unhealthy
    since       TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS failure_ignores (
    subject_kind TEXT        NOT NULL,
    subject_id   UUID        NOT NULL,
    stage        TEXT        NOT NULL,
    ignored_by   TEXT,
    ignored_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subject_kind, subject_id, stage)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS item_requests (
    id           BIGSERIAL   PRIMARY KEY,
    video_id     UUID        NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    action       TEXT        NOT NULL CHECK (action IN ('redownload')),
    requested_by TEXT,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    done_at      TIMESTAMPTZ,
    result       TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS item_requests_open_idx
    ON item_requests (action, video_id) WHERE done_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS item_checks (
    video_id   UUID        PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    ok         BOOLEAN     NOT NULL,
    error      TEXT,
    host       TEXT,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS missing_confirmed_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement)
VALUES
    ('access_check', 'Access check', ARRAY['video']::text[], 'nas_read', 'media', 13, 1, 0, 0, 1, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto', 'any')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- the workers probe, check and download; only an admin (the API) ignores
        REVOKE ALL ON failure_ignores FROM maktaba_worker;
        GRANT SELECT ON failure_ignores TO maktaba_worker;
        REVOKE ALL ON item_requests FROM maktaba_worker;
        GRANT SELECT, UPDATE ON item_requests TO maktaba_worker;
        GRANT SELECT, INSERT, UPDATE ON dependency_health TO maktaba_worker;
        GRANT SELECT, INSERT, UPDATE ON item_checks TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'access_check'
   AND NOT EXISTS (SELECT 1 FROM processing_jobs WHERE stage = 'access_check');
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN IF EXISTS missing_confirmed_at;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS item_checks;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS item_requests;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS failure_ignores;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS dependency_health;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS processing_jobs_parked_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP COLUMN IF EXISTS parked_on;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP COLUMN IF EXISTS failure_class;
-- +goose StatementEnd
