-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0015 (Epic 4 / plan-04-03) — subtitle file registry.
--
-- One row per subtitle file written under
-- ``~/.maktaba/cache/subtitles/{video_id}/{language}.{format}``.
-- Three sources land here:
--
--   - ``embedded`` — extracted from the source container by ffmpeg
--     (``mov_text``, ``subrip``, ``ass``, …).
--   - ``generated`` — rendered from ``transcript_segments`` rows by the
--     subtitle generator (Story 4.x).
--   - ``external`` — sidecar files discovered next to the video on disk
--     (``movie.en.srt``, ``movie.ar.vtt``).
--
-- The partial unique index covers (video_id, language, format) for live
-- rows only; soft-deleted entries are tombstoned via ``deleted_at`` so
-- the manager can detect prior writes during atomic replace.
--
CREATE TABLE IF NOT EXISTS subtitle_files (
    id            BIGSERIAL    PRIMARY KEY,
    video_id      UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    transcript_id UUID         REFERENCES transcripts(id) ON DELETE SET NULL,
    language      TEXT         NOT NULL,
    format        TEXT         NOT NULL CHECK (format IN ('srt','vtt')),
    source        TEXT         NOT NULL CHECK (source IN ('embedded','generated','external')),
    path          TEXT         NOT NULL,
    byte_size     BIGINT,
    sha256        TEXT,
    is_embedded   BOOLEAN      NOT NULL DEFAULT false,
    is_external   BOOLEAN      NOT NULL DEFAULT false,
    metadata      JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS subtitle_files_active_unique
    ON subtitle_files (video_id, language, format, source)
    WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS subtitle_files_video_idx
    ON subtitle_files (video_id) WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION subtitle_files_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(
        'subtitle_files.committed',
        json_build_object(
            'video_id', NEW.video_id,
            'language', NEW.language,
            'format', NEW.format,
            'source', NEW.source
        )::text
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS subtitle_files_notify_trg ON subtitle_files;
CREATE TRIGGER subtitle_files_notify_trg
    AFTER INSERT ON subtitle_files
    FOR EACH ROW EXECUTE FUNCTION subtitle_files_notify();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS subtitle_files_notify_trg ON subtitle_files;
DROP FUNCTION IF EXISTS subtitle_files_notify();
DROP TABLE IF EXISTS subtitle_files;
-- +goose StatementEnd
