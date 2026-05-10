-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0025 (Story 5.5 / plan-05-05) — incremental indexing schema.
--
-- Adds:
--   - `transcript_units.indexed_at_in_chroma` — separate watermark from
--     `indexed_at` so the live indexer can complete FTS-only writes
--     while batch Chroma indexing runs at the `index` stage.
--   - `vector_index_dead_letter` — durable queue for units whose
--     Chroma write failed, drained by the nightly reaper.
--
-- `transcripts.last_indexed_segment_seq` already exists from slot 0012;
-- no further alteration needed.
--
ALTER TABLE transcript_units
    ADD COLUMN IF NOT EXISTS indexed_at_in_chroma TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_units_unindexed_chroma_idx
    ON transcript_units (transcript_id) WHERE indexed_at_in_chroma IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS vector_index_dead_letter (
    id                 BIGSERIAL    PRIMARY KEY,
    unit_id            BIGINT       NOT NULL REFERENCES transcript_units(id) ON DELETE CASCADE,
    library_id         UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    transcript_id      BIGINT       NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    attempts           INT          NOT NULL DEFAULT 0,
    last_error         TEXT,
    last_attempted_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (unit_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS vector_dlq_library_idx
    ON vector_index_dead_letter (library_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS vector_dlq_library_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS vector_index_dead_letter;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_units_unindexed_chroma_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE transcript_units DROP COLUMN IF EXISTS indexed_at_in_chroma;
-- +goose StatementEnd
