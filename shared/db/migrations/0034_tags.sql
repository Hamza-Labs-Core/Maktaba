-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0034 (Epic 7 / Story 7.14) — tag taxonomy.
--
-- ``name_norm`` is NFC-normalised + casefolded — the uniqueness key
-- for case- and accent-insensitive dedup. ``name`` preserves the
-- original casing for display.
--
CREATE TABLE IF NOT EXISTS tags (
    id         UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT         NOT NULL,
    name_norm  TEXT         NOT NULL UNIQUE,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS video_tags (
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    tag_id   UUID NOT NULL REFERENCES tags(id)   ON DELETE CASCADE,
    added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (video_id, tag_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS video_tags_tag_idx
    ON video_tags (tag_id, video_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS video_tags_tag_idx;
DROP TABLE IF EXISTS video_tags;
DROP TABLE IF EXISTS tags;
-- +goose StatementEnd
