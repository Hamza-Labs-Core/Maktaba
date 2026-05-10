-- +goose Up
-- +goose StatementBegin
--
-- Slot 0020 (Story 5.2 / plan-05-02) — SQLite FTS5 virtual table.
--
-- Contentless FTS5 indexed by `unit_id`; `text` column holds the raw
-- (non-normalized) form so per-row ranking and snippets remain accurate.
-- Triggers in slot 0022 keep this table in sync with `transcript_units`,
-- applying `arabic_normalize()` (Python-side, via custom user function).
--
-- `tokenize = 'unicode61 remove_diacritics 2'` is the most aggressive
-- diacritic-stripping mode SQLite ships with — it covers Arabic
-- combining marks (tashkeel) at the tokenizer level, so a query for
-- `الحمد` matches `الحَمد`.
--
CREATE VIRTUAL TABLE IF NOT EXISTS transcripts_fts USING fts5(
    text,
    transcript_id UNINDEXED,
    unit_id       UNINDEXED,
    language      UNINDEXED,
    tokenize = 'unicode61 remove_diacritics 2'
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS transcripts_fts;
-- +goose StatementEnd
