-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0017 (Story 5.1 / plan-05-01) — transcript_units chunk table.
--
-- Chunks transcript_segments into ~200-char passages for the FTS and
-- vector indexes. `segment_ids` is an ordered JSONB array of source
-- segment ids; `segment_ids[0]` resolves back to the unit's start_sec.
--
-- `indexed_at` is set when both FTS and Chroma have committed the unit.
-- A separate `indexed_at_in_chroma` is added by slot 0025 to allow the
-- live indexer to write FTS-only without staking the column.
--
CREATE TABLE IF NOT EXISTS transcript_units (
    id              BIGSERIAL    PRIMARY KEY,
    transcript_id   BIGINT       NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    seq             INTEGER      NOT NULL CHECK (seq >= 1),
    start_sec       REAL         NOT NULL,
    end_sec         REAL         NOT NULL,
    text            TEXT         NOT NULL,
    language        TEXT         NOT NULL,
    segment_ids     JSONB        NOT NULL,
    indexed_at      TIMESTAMPTZ,
    metadata        JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),

    UNIQUE (transcript_id, seq),
    CHECK (start_sec <= end_sec)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Story 5.1 (REVIEW §6.3): transcript_units(language) index.
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_units_lang_idx
    ON transcript_units (language);
-- +goose StatementEnd

-- +goose StatementBegin
-- Plan 5.5: incremental indexer scans for units that are missing from
-- one of the two indexes.
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_units_unindexed_idx
    ON transcript_units (transcript_id) WHERE indexed_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Plan 5.4: timestamp-window query used for unit→segment resolution.
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_units_tid_start_idx
    ON transcript_units (transcript_id, start_sec);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_units_tid_start_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_units_unindexed_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_units_lang_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS transcript_units;
-- +goose StatementEnd
