-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0031 (Epic 5 / search suggestions) — search history corpus.
--
-- One row per executed search, used to power autocomplete/typeahead and
-- "recent searches" surfaces. ``query_norm`` is the lowercased,
-- unaccented form used as the prefix key; the original ``query`` is
-- preserved for display.
--
-- ``hits`` counts the number of distinct executions of the same
-- normalised query — incremented by an UPSERT so the table stays small.
-- ``last_used_at`` is the recency input for ranking.
--
CREATE TABLE IF NOT EXISTS search_history (
    id            BIGSERIAL    PRIMARY KEY,
    user_id       UUID         REFERENCES users(id) ON DELETE CASCADE,
    query         TEXT         NOT NULL,
    query_norm    TEXT         NOT NULL,
    hits          INTEGER      NOT NULL DEFAULT 1,
    result_count  INTEGER,
    first_used_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_used_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (user_id, query_norm)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS search_history_prefix_idx
    ON search_history (query_norm text_pattern_ops);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS search_history_recent_idx
    ON search_history (user_id, last_used_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS search_history;
-- +goose StatementEnd
