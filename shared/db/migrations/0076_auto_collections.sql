-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0076 (Epic 26 / Story 26.4) — auto-built smart collections.
--
-- Auto collections are ordinary `collections` rows (slot 0033) with
-- `origin='auto'` and an `auto_rule` describing how they were built, so
-- the existing collection serving path needs no change (key decision in
-- the epic README). `dismissed_at` lets a user hide an auto collection
-- without deleting it. The only genuinely new surface is the suggestion
-- lifecycle table `collection_suggestions` (suggested → accepted →
-- dismissed).
--
ALTER TABLE collections ADD COLUMN IF NOT EXISTS origin TEXT NOT NULL DEFAULT 'user';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections ADD COLUMN IF NOT EXISTS auto_rule JSONB;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections ADD COLUMN IF NOT EXISTS dismissed_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS collection_suggestions (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id  UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name        TEXT         NOT NULL,
    kind        TEXT         NOT NULL,
    smart_query JSONB        NOT NULL DEFAULT '{}'::jsonb,
    status      TEXT         NOT NULL DEFAULT 'suggested'
                             CHECK (status IN ('suggested','accepted','dismissed')),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    decided_at  TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS collection_suggestions_library_idx
    ON collection_suggestions (library_id, status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS collection_suggestions_library_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS collection_suggestions;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections DROP COLUMN IF EXISTS dismissed_at;
ALTER TABLE collections DROP COLUMN IF EXISTS auto_rule;
ALTER TABLE collections DROP COLUMN IF EXISTS origin;
-- +goose StatementEnd
