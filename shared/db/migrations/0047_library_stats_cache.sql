-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0047 (Epic 9 / Story 9.7) — denormalized stats cache.
--
CREATE TABLE IF NOT EXISTS library_stats_cache (
    library_id              UUID         PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
    total_videos            INTEGER      NOT NULL DEFAULT 0,
    total_duration_sec      BIGINT       NOT NULL DEFAULT 0,
    source_size_bytes       BIGINT       NOT NULL DEFAULT 0,
    derived_size_bytes      BIGINT       NOT NULL DEFAULT 0,
    by_state_jsonb          JSONB        NOT NULL DEFAULT '{}'::jsonb,
    by_language_jsonb       JSONB        NOT NULL DEFAULT '{}'::jsonb,
    by_content_type_jsonb   JSONB        NOT NULL DEFAULT '{}'::jsonb,
    jobs_jsonb              JSONB        NOT NULL DEFAULT '{}'::jsonb,
    last_sweep_jsonb        JSONB,
    updated_at              TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS library_stats_cache;
-- +goose StatementEnd
