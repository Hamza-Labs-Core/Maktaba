-- +goose Up
-- +goose StatementBegin
--
-- Slot 0047 (Epic 9 / Story 9.7) — denormalized stats cache.
--
-- Backs the < 50 ms `GET /api/libraries/{id}/stats` target. Maintained
-- by triggers on `videos`, `processing_jobs`, and the sweep finalizer.
-- A nightly reconciliation job recomputes from source tables.
--
CREATE TABLE IF NOT EXISTS library_stats_cache (
    library_id              TEXT     PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
    total_videos            INTEGER  NOT NULL DEFAULT 0,
    total_duration_sec      INTEGER  NOT NULL DEFAULT 0,
    source_size_bytes       INTEGER  NOT NULL DEFAULT 0,
    derived_size_bytes      INTEGER  NOT NULL DEFAULT 0,
    by_state_jsonb          TEXT     NOT NULL DEFAULT '{}',
    by_language_jsonb       TEXT     NOT NULL DEFAULT '{}',
    by_content_type_jsonb   TEXT     NOT NULL DEFAULT '{}',
    jobs_jsonb              TEXT     NOT NULL DEFAULT '{}',
    last_sweep_jsonb        TEXT,
    updated_at              TEXT     NOT NULL DEFAULT (datetime('now'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS library_stats_cache;
-- +goose StatementEnd
