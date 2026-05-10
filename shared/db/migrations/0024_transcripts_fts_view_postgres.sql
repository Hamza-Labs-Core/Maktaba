-- +goose Up
-- +goose StatementBegin
--
-- Slot 0024 (Story 5.2 / plan-05-02) — `transcripts_fts` compatibility
-- view (Postgres).
--
-- Both engines expose a logical `transcripts_fts` table/view with the
-- same column shape so application code does not branch on dialect:
--   (rowid, text, transcript_id, unit_id, language)
--
-- The Postgres side is a view over `transcript_units`; ranking is done
-- with `ts_rank_cd(tsv, query)` against the GIN index from slot 0023.
--
CREATE OR REPLACE VIEW transcripts_fts AS
SELECT
    id            AS rowid,
    text          AS text,
    transcript_id AS transcript_id,
    id            AS unit_id,
    language      AS language,
    tsv           AS tsv
FROM transcript_units;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS transcripts_fts;
-- +goose StatementEnd
