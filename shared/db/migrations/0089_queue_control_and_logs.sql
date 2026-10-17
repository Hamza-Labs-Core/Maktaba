-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0089 (plan 06-11 section 10.3, workstream A) - queue control and logs.
--
--   queue_control   singleton row; paused=true stops every claim.
--   job_events      append-only log, one row per meaningful event per job.
--                   No foreign key to processing_jobs: logs outlive pruned
--                   job rows.
--   health_samples  NAS / CPU / network samples written by each worker.
--   jobs.control    pg_notify fired when worker_pools, job_stages or
--                   queue_control change, so workers re-read pool sizes and
--                   pause flags without waiting for the next poll.
--
-- Runs outside a transaction because the indexes are built CONCURRENTLY.
--
CREATE TABLE IF NOT EXISTS queue_control (
    id         SMALLINT PRIMARY KEY CHECK (id = 1),
    paused     BOOLEAN NOT NULL DEFAULT false,
    paused_at  TIMESTAMPTZ,
    paused_by  TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO queue_control (id, paused) VALUES (1, false) ON CONFLICT (id) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS job_events (
    id            BIGSERIAL PRIMARY KEY,
    job_id        BIGINT NOT NULL,
    subject_kind  TEXT   NOT NULL,
    subject_id    UUID   NOT NULL,
    library_id    UUID,
    stage         TEXT   NOT NULL,
    at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    level         TEXT   NOT NULL CHECK (level IN ('debug','info','warn','error')),
    kind          TEXT   NOT NULL CHECK (kind IN (
                      'enqueued','coalesced','rerun_requested','claimed',
                      'step','skipped','done','failed','retry_scheduled','cancelled','paused',
                      'resumed','reaped','triggered','requested','review','applied','imported')),
    message       TEXT   NOT NULL,
    data          JSONB
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Worker registry (multi-host): each worker process declares its host, the
-- stages it serves, its pools and a heartbeat, so the UI can show which hosts
-- are online and which stages have no live worker.
CREATE TABLE IF NOT EXISTS workers (
    id            TEXT PRIMARY KEY,
    host          TEXT NOT NULL,
    stages        TEXT[] NOT NULL DEFAULT '{}',
    pools         TEXT[] NOT NULL DEFAULT '{}',
    path_map      JSONB NOT NULL DEFAULT '{}'::jsonb,
    version       TEXT,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Server-side extensions (plan 06-11 notes N3): enabled flag and settings.
-- Secrets are stored here but never returned by the API (the manifest in
-- code flags which keys are secret). Env vars only bootstrap the first row.
CREATE TABLE IF NOT EXISTS extensions (
    name        TEXT PRIMARY KEY,
    enabled     BOOLEAN NOT NULL DEFAULT false,
    settings    JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS health_samples (
    at      TIMESTAMPTZ NOT NULL,
    host    TEXT NOT NULL,
    metric  TEXT NOT NULL,
    value   DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (metric, host, at)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS job_events_job_idx ON job_events (job_id, id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS job_events_stage_idx ON job_events (stage, id DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS job_events_subject_idx
    ON job_events (subject_kind, subject_id, id DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS job_events_library_idx
    ON job_events (library_id, id DESC) WHERE library_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS job_events_at_idx ON job_events (at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS health_samples_at_idx ON health_samples (at);
-- +goose StatementEnd

-- +goose StatementBegin
-- TG_ARGV[0] names the primary key column of the table the trigger is on.
CREATE OR REPLACE FUNCTION jobs_control_notify() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(
        'jobs.control',
        json_build_object(
            'table', TG_TABLE_NAME,
            'key',   to_jsonb(NEW) ->> TG_ARGV[0]
        )::text
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER worker_pools_control_trg
    AFTER INSERT OR UPDATE ON worker_pools
    FOR EACH ROW EXECUTE FUNCTION jobs_control_notify('pool');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER job_stages_control_trg
    AFTER INSERT OR UPDATE ON job_stages
    FOR EACH ROW EXECUTE FUNCTION jobs_control_notify('stage');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER queue_control_control_trg
    AFTER INSERT OR UPDATE ON queue_control
    FOR EACH ROW EXECUTE FUNCTION jobs_control_notify('id');
-- +goose StatementEnd

-- +goose Down
-- +goose NO TRANSACTION
-- +goose StatementBegin
DROP TRIGGER IF EXISTS queue_control_control_trg ON queue_control;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS job_stages_control_trg ON job_stages;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS worker_pools_control_trg ON worker_pools;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS jobs_control_notify();
-- +goose StatementEnd

DROP INDEX IF EXISTS health_samples_at_idx;
DROP INDEX IF EXISTS job_events_at_idx;
DROP INDEX IF EXISTS job_events_library_idx;
DROP INDEX IF EXISTS job_events_subject_idx;
DROP INDEX IF EXISTS job_events_stage_idx;
DROP INDEX IF EXISTS job_events_job_idx;

-- +goose StatementBegin
DROP TABLE IF EXISTS extensions;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS workers;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS health_samples;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS job_events;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS queue_control;
-- +goose StatementEnd
