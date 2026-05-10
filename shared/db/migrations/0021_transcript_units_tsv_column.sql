-- +goose Up
-- +goose StatementBegin
--
-- Slot 0021 (Story 5.2 / plan-05-02) — `transcript_units.tsv` generated
-- column.
--
-- STORED so the GIN index (slot 0023) actually has something to index;
-- VIRTUAL would force per-query recomputation. Both the language config
-- and the normalization are inlined via slot 0019's IMMUTABLE helpers
-- so the column is deterministic.
--
ALTER TABLE transcript_units
    ADD COLUMN IF NOT EXISTS tsv tsvector
    GENERATED ALWAYS AS (
        to_tsvector(
            COALESCE(language_to_regconfig(language), 'pg_catalog.simple'::regconfig),
            maktaba_normalize(text)
        )
    ) STORED;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE transcript_units DROP COLUMN IF EXISTS tsv;
-- +goose StatementEnd
