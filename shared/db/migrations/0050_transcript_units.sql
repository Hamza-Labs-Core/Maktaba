-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0050 (Story 5.1 / plan-05-01) — `transcript_units` indexing table.
--
-- Search-indexable token unit (typically a transcript segment or sentence
-- chunk; populated by the `index` stage). Decoupled from
-- `transcript_segments` so chunking strategy can change without rewriting
-- STT outputs (architecture §8.1).
--
-- The `embedding_id` column points at the row's id in the external vector
-- store (Chroma); a NULL value means the unit exists in Postgres but has
-- not yet been embedded. The optional `tsv` generated column mirrors the
-- shape used by `transcript_segments` (slot 0016) so the same FTS
-- configuration can be applied later without a second migration.
--
CREATE TABLE IF NOT EXISTS transcript_units (
    id              BIGSERIAL PRIMARY KEY,
    transcript_id   UUID NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    video_id        UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    segment_id      BIGINT REFERENCES transcript_segments(id) ON DELETE CASCADE,
    unit_index      INT NOT NULL,
    start_sec       REAL NOT NULL,
    end_sec         REAL NOT NULL,
    text            TEXT NOT NULL,
    language        TEXT,
    embedding_id    TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (transcript_id, unit_index)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_units_video_idx
    ON transcript_units (video_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_units_segment_idx
    ON transcript_units (segment_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_units_time_idx
    ON transcript_units (transcript_id, start_sec);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS transcript_units;
-- +goose StatementEnd
