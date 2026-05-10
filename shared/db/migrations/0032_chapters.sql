-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0032 (Epic 7 / Story 7.7) — chapter list per video.
--
-- Three provenance sources:
--   - ``embedded``   — extracted from the container at probe time.
--   - ``inferred``   — produced by topic-shift analysis (Epic 9.18).
--   - ``manual``     — user-entered through the API.
--
-- Ordering is by ``seq`` (1-based). ``end_sec`` may be NULL for the
-- final chapter (caller fills with video duration at read time).
--
CREATE TABLE IF NOT EXISTS chapters (
    id         BIGSERIAL    PRIMARY KEY,
    video_id   UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    seq        INTEGER      NOT NULL,
    start_sec  REAL         NOT NULL CHECK (start_sec >= 0),
    end_sec    REAL         CHECK (end_sec IS NULL OR end_sec > start_sec),
    title      TEXT         NOT NULL,
    source     TEXT         NOT NULL DEFAULT 'manual'
               CHECK (source IN ('embedded','inferred','manual')),
    metadata   JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (video_id, seq)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS chapters_video_idx
    ON chapters (video_id, seq);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS chapters_video_idx;
DROP TABLE IF EXISTS chapters;
-- +goose StatementEnd
