-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0016 (Epic 5 / search) — full-text search over transcript_segments.
--
-- Approach: a generated ``search_tsv`` tsvector column on
-- ``transcript_segments`` populated via the ``maktaba_search`` text-search
-- configuration. The configuration is a `simple` mapping plus an
-- unaccent step so it works for both Arabic (where most stemmers are
-- unhelpful) and English (where stemming would over-collapse forms in a
-- mixed corpus).
--
-- The ``simple`` config preserves all tokens; combined with
-- ``unaccent`` it gives diacritic-insensitive matching while keeping
-- the implementation portable across PG installations that may not
-- ship the Arabic snowball stemmer.
--
-- A GIN index on the column powers fast prefix and phrase queries.
--
CREATE EXTENSION IF NOT EXISTS unaccent;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_ts_config WHERE cfgname = 'maktaba_search'
    ) THEN
        EXECUTE 'CREATE TEXT SEARCH CONFIGURATION maktaba_search ( COPY = simple )';
        EXECUTE 'ALTER TEXT SEARCH CONFIGURATION maktaba_search '
             || 'ALTER MAPPING FOR hword, hword_part, word '
             || 'WITH unaccent, simple';
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE transcript_segments
    ADD COLUMN IF NOT EXISTS search_tsv tsvector
    GENERATED ALWAYS AS (to_tsvector('maktaba_search', coalesce(text, ''))) STORED;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_segments_search_tsv_idx
    ON transcript_segments USING GIN (search_tsv);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_segments_search_tsv_idx;
ALTER TABLE transcript_segments DROP COLUMN IF EXISTS search_tsv;
DROP TEXT SEARCH CONFIGURATION IF EXISTS maktaba_search;
-- +goose StatementEnd
