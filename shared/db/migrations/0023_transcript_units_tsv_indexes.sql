-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0023 (Story 5.2 / plan-05-02) — GIN index on transcript_units.tsv.
--
-- CONCURRENTLY because the column may already hold rows by the time the
-- migration lands; building inline would block writers.
--
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_units_tsv_idx
    ON transcript_units USING GIN (tsv);
-- +goose StatementEnd

-- +goose StatementBegin
-- Trigram index on the raw text — supports prefix + fuzzy fallback for
-- the suggest service (slot 0027). Stripping diacritics first via
-- maktaba_normalize is symmetric with how queries are processed.
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_units_text_trgm_idx
    ON transcript_units USING GIN ((maktaba_normalize(text)) gin_trgm_ops);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_units_text_trgm_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_units_tsv_idx;
-- +goose StatementEnd
