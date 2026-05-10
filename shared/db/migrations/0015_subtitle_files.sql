-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0015 (Story 4.3 / plan-04-03) — canonical subtitle_files table.
--
-- Single migration that ships the full shape: external + embedded +
-- generated, with all indexes and the NOTIFY trigger. Plans 4.1
-- (generate from segments) and 4.4 (embedded extraction) write rows here
-- but do NOT add columns; the schema is locked here.
--
-- Resolves REVIEW §1.1.c (`is_embedded` ownership).
--
CREATE TABLE IF NOT EXISTS subtitle_files (
    id              BIGSERIAL    PRIMARY KEY,
    video_id        UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    transcript_id   BIGINT       REFERENCES transcripts(id) ON DELETE SET NULL,
    format          TEXT         NOT NULL CHECK (format IN ('srt','vtt','ass','ssa')),
    language        TEXT         NOT NULL,
    path            TEXT         NOT NULL,
    is_external     BOOLEAN      NOT NULL DEFAULT FALSE,
    is_embedded     BOOLEAN      NOT NULL DEFAULT FALSE,
    is_default      BOOLEAN      NOT NULL DEFAULT FALSE,
    flags           JSONB        NOT NULL DEFAULT '{}'::jsonb,
    track_index     INT,
    size_bytes      BIGINT,
    mtime_ns        BIGINT,
    metadata        JSONB        NOT NULL DEFAULT '{}'::jsonb,
    revived_count   INT          NOT NULL DEFAULT 0,
    deleted_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CHECK (NOT (is_external AND is_embedded)),
    CHECK ((is_embedded = FALSE) OR (is_embedded = TRUE AND track_index IS NOT NULL))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS subtitle_files_video_lang_idx
    ON subtitle_files (video_id, language);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS subtitle_files_video_default_idx
    ON subtitle_files (video_id, is_default DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- One generated/internal sidecar per (video, format, language); external
-- and embedded artifacts coexist with multiple of the same key.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS subtitle_files_internal_uq
    ON subtitle_files (video_id, format, language)
    WHERE is_external = FALSE AND is_embedded = FALSE;
-- +goose StatementEnd

-- +goose StatementBegin
-- One row per (video, embedded track index).
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS subtitle_files_embedded_uq
    ON subtitle_files (video_id, track_index)
    WHERE is_embedded = TRUE;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION subtitle_files_changed_notify() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(
        'subtitle_files.changed',
        json_build_object(
            'video_id',   NEW.video_id,
            'subtitle_id', NEW.id,
            'format',     NEW.format,
            'language',   NEW.language,
            'op',         TG_OP
        )::text
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS subtitle_files_changed_notify_trg ON subtitle_files;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER subtitle_files_changed_notify_trg
    AFTER INSERT OR UPDATE ON subtitle_files
    FOR EACH ROW
    EXECUTE FUNCTION subtitle_files_changed_notify();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS subtitle_files_changed_notify_trg ON subtitle_files;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS subtitle_files_changed_notify();
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS subtitle_files_embedded_uq;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS subtitle_files_internal_uq;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS subtitle_files_video_default_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS subtitle_files_video_lang_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS subtitle_files;
-- +goose StatementEnd
