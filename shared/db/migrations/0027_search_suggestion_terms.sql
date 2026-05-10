-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0027 (Story 5.6 / plan-05-06) — search_suggestion_terms typeahead
-- corpus.
--
-- Populated by the nightly ngram extractor (`suggest_build`). Prefix
-- queries hit `term_normalized text_pattern_ops`; fuzzy fallback uses
-- the trigram index on the display form.
--
CREATE TABLE IF NOT EXISTS search_suggestion_terms (
    id              BIGSERIAL    PRIMARY KEY,
    library_id      UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    term            TEXT         NOT NULL,
    term_normalized TEXT         NOT NULL,
    ngram           SMALLINT     NOT NULL CHECK (ngram BETWEEN 2 AND 4),
    frequency       INTEGER      NOT NULL CHECK (frequency >= 1),
    doc_frequency   INTEGER      NOT NULL CHECK (doc_frequency >= 1),
    last_seen_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (library_id, term_normalized)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS search_suggestion_terms_prefix_idx
    ON search_suggestion_terms (library_id, term_normalized text_pattern_ops);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS search_suggestion_terms_trgm_idx
    ON search_suggestion_terms USING GIN (term gin_trgm_ops);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS search_suggestion_terms_trgm_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS search_suggestion_terms_prefix_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS search_suggestion_terms;
-- +goose StatementEnd
