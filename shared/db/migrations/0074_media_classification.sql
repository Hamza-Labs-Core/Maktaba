-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0074 (Epic 26 / Story 26.2) — per-video classification + the
-- canonical entity tables.
--
-- `video_classification` is the 1:1 refined content type + language +
-- model scores produced by transcript topic/entity extraction (26.2).
-- `media_entities` is the canonical people/places/orgs table (homonyms
-- are distinct rows); `video_entities` is the M:N link with the
-- in-transcript offsets. Context cards (26.9) join `video_entities` on
-- the canonical `entity_id` so two different people sharing a name don't
-- cross-link.
--
CREATE TABLE IF NOT EXISTS video_classification (
    video_id      UUID         PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    content_type  TEXT,
    language      TEXT,
    scores        JSONB        NOT NULL DEFAULT '{}'::jsonb,
    model_version TEXT         NOT NULL DEFAULT 'v1',
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS media_entities (
    id         UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    kind       TEXT         NOT NULL CHECK (kind IN ('PER','LOC','ORG')),
    name       TEXT         NOT NULL,
    name_norm  TEXT         NOT NULL,
    metadata   JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS video_entities (
    video_id   UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    entity_id  UUID NOT NULL REFERENCES media_entities(id) ON DELETE CASCADE,
    role       TEXT,
    offsets    JSONB NOT NULL DEFAULT '[]'::jsonb,
    PRIMARY KEY (video_id, entity_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS media_entities_kind_norm_idx
    ON media_entities (kind, name_norm);
-- +goose StatementEnd

-- +goose StatementBegin
-- Shared-cast / shared-entity lookups for context cards (26.9) start
-- from an entity and fan out to videos.
CREATE INDEX CONCURRENTLY IF NOT EXISTS video_entities_entity_idx
    ON video_entities (entity_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS video_entities_entity_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS media_entities_kind_norm_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS video_entities;
DROP TABLE IF EXISTS media_entities;
DROP TABLE IF EXISTS video_classification;
-- +goose StatementEnd
