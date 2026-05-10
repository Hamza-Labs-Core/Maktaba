-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0026 (Story 5.7 / plan-05-07) — chapters table.
--
-- One row per inferred (or embedded / manual) chapter. The `source`
-- discriminator lets later epics (09-18) extend chapters without
-- changing this shape.
--
CREATE TABLE IF NOT EXISTS chapters (
    id              BIGSERIAL    PRIMARY KEY,
    video_id        UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    transcript_id   BIGINT       REFERENCES transcripts(id) ON DELETE CASCADE,
    seq             INTEGER      NOT NULL CHECK (seq >= 0),
    start_sec       REAL         NOT NULL CHECK (start_sec >= 0),
    end_sec         REAL         NOT NULL,
    title           TEXT,
    source          TEXT         NOT NULL DEFAULT 'inferred'
                                 CHECK (source IN ('inferred','embedded','manual')),
    lang            TEXT,
    confidence      REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    metadata        JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),

    UNIQUE (video_id, source, seq),
    CHECK (end_sec >= start_sec)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS chapters_video_start_idx
    ON chapters (video_id, start_sec);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS chapters_video_start_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS chapters;
-- +goose StatementEnd
