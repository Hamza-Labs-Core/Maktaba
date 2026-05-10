-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0057 (plan-24-07) — integrity_checks audit table. One row per
-- per-video integrity probe; the verifier writes a new row each pass
-- so we keep history for trend analysis.
--
CREATE TABLE IF NOT EXISTS integrity_checks (
    id              UUID         PRIMARY KEY,
    video_id        UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    checked_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    file_present    BOOLEAN      NOT NULL,
    size_bytes      BIGINT,
    content_hash    TEXT,
    segments_count  INTEGER,
    transcripts_ok  BOOLEAN,
    error           TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS integrity_checks_video
    ON integrity_checks (video_id, checked_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS integrity_checks_problems
    ON integrity_checks (checked_at DESC)
    WHERE NOT file_present OR error IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS integrity_checks_problems;
DROP INDEX IF EXISTS integrity_checks_video;
DROP TABLE IF EXISTS integrity_checks;
-- +goose StatementEnd
