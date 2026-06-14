-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0079 (Epic 26 / Story 26.7) — the out-of-band `enrich` queue +
-- the debounced group-pass marker.
--
-- `enrich` is deliberately NOT a `processing_jobs.stage` and NOT a
-- `videos.state`: web enrichment is networked, rate-limited, and must
-- never hold a video out of READY (epic key decision). It gets its own
-- queue table with its own status/attempts/backoff, claimed by a
-- dedicated worker (`maktaba_pipeline.enrich.worker`). `not_before`
-- carries both retry backoff and rate-limit/quota deferral; a deferred
-- job is rescheduled without consuming an attempt.
--
-- `library_group_pending` persists the "a group pass is owed for this
-- library" marker so a debounced series-detect + auto-collection pass
-- still runs after a restart (the in-memory debounce timer does not
-- survive a crash; this row does).
--
CREATE TABLE IF NOT EXISTS enrich_jobs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id    UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending','running','done','deferred','failed')),
    force       BOOLEAN NOT NULL DEFAULT false,
    attempts    INTEGER NOT NULL DEFAULT 0,
    not_before  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS library_group_pending (
    library_id UUID PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
    marked_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- At most one open (pending/deferred) enrich job per video; the worker
-- claims by (status, not_before).
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS enrich_jobs_open_video_idx
    ON enrich_jobs (video_id) WHERE status IN ('pending','running','deferred');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS enrich_jobs_claim_idx
    ON enrich_jobs (status, not_before) WHERE status IN ('pending','deferred');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS enrich_jobs_claim_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS enrich_jobs_open_video_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS library_group_pending;
DROP TABLE IF EXISTS enrich_jobs;
-- +goose StatementEnd
