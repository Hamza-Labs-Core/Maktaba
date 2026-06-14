-- +goose Up
-- +goose StatementBegin
-- SQLite parity sibling for slot 0074 (Story 26.2). JSONB → TEXT (JSON
-- string); UUID → TEXT.
CREATE TABLE IF NOT EXISTS video_classification (
    video_id      TEXT    PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    content_type  TEXT,
    language      TEXT,
    scores        TEXT    NOT NULL DEFAULT '{}',
    model_version TEXT    NOT NULL DEFAULT 'v1',
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS media_entities (
    id         TEXT    PRIMARY KEY,
    kind       TEXT    NOT NULL CHECK (kind IN ('PER','LOC','ORG')),
    name       TEXT    NOT NULL,
    name_norm  TEXT    NOT NULL,
    metadata   TEXT    NOT NULL DEFAULT '{}',
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS video_entities (
    video_id   TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    entity_id  TEXT NOT NULL REFERENCES media_entities(id) ON DELETE CASCADE,
    role       TEXT,
    offsets    TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (video_id, entity_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS media_entities_kind_norm_idx
    ON media_entities (kind, name_norm);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS video_entities_entity_idx
    ON video_entities (entity_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS video_entities_entity_idx;
DROP INDEX IF EXISTS media_entities_kind_norm_idx;
DROP TABLE IF EXISTS video_entities;
DROP TABLE IF EXISTS media_entities;
DROP TABLE IF EXISTS video_classification;
-- +goose StatementEnd
