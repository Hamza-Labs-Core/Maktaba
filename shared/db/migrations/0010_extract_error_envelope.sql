-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0010 (Story 2.3 / plan-02-03) — extract-stage audio cache.
--
-- Story 2.3 AC-3 calls for a structured `error` envelope on
-- `processing_jobs`. The error column was created as plain TEXT in
-- slot 0002; rather than rewrite the column type (which the migration
-- linter forbids on a populated production table), the worker
-- json-encodes the envelope and stores it as TEXT. A future migration
-- can ship the column-swap recipe (new column + backfill + swap) when
-- there's actually data to migrate.
--
-- The `audio_cache` table holds temporary WAVs for STT backends with
-- `requires_file = true`. The path is derived from the video's
-- `content_hash`; the row tracks the cache lifetime so the cleanup
-- pass on terminal job state can find what to delete.
--
CREATE TABLE IF NOT EXISTS audio_cache (
    content_hash    TEXT         PRIMARY KEY,
    video_id        UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    audio_track_id  BIGINT       NOT NULL REFERENCES audio_tracks(id) ON DELETE CASCADE,
    path            TEXT         NOT NULL,
    bytes           BIGINT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS audio_cache_video_idx
    ON audio_cache (video_id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audio_tracks
    ADD COLUMN IF NOT EXISTS last_extracted_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audio_cache;
ALTER TABLE audio_tracks DROP COLUMN IF EXISTS last_extracted_at;
-- +goose StatementEnd
