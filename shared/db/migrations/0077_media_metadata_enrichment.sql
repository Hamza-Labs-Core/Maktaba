-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0077 (Epic 26 / Story 26.5) — enrichment candidates + the shared
-- web-metadata cache.
--
-- `media_metadata_enrichment` is a *staging* table: each row is one
-- provider's candidate match for a video (TMDb/OMDb/YouTube/MusicBrainz/
-- Wikidata). `mapped` is the provider's fields normalized to Maktaba's
-- shape (title, description, poster_path, rating, cast, genres, …).
-- Nothing here touches `videos` until the user accepts (Story 26.6); the
-- stored stable `external_id` makes a re-enrich an idempotent refresh by
-- id rather than a re-search. `is_accepted` records the promoted
-- candidate; `is_dismissed` (added in slot 0078) hides candidates.
--
-- `web_metadata_cache` is the shared on-disk response cache (TTL'd) that
-- every adapter reads/writes through the common rate-limited WebClient.
--
CREATE TABLE IF NOT EXISTS media_metadata_enrichment (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id    UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    provider    TEXT         NOT NULL,
    external_id TEXT         NOT NULL,
    mapped      JSONB        NOT NULL DEFAULT '{}'::jsonb,
    confidence  DOUBLE PRECISION NOT NULL DEFAULT 0,
    is_accepted BOOLEAN      NOT NULL DEFAULT false,
    fetched_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (video_id, provider, external_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS web_metadata_cache (
    cache_key  TEXT         PRIMARY KEY,
    provider   TEXT         NOT NULL,
    response   JSONB        NOT NULL,
    fetched_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ  NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
-- The enrichment review panel + context card both load candidates by
-- video, ranked by confidence.
CREATE INDEX CONCURRENTLY IF NOT EXISTS media_metadata_enrichment_video_idx
    ON media_metadata_enrichment (video_id, confidence DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS web_metadata_cache_expiry_idx
    ON web_metadata_cache (expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS web_metadata_cache_expiry_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS media_metadata_enrichment_video_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS web_metadata_cache;
DROP TABLE IF EXISTS media_metadata_enrichment;
-- +goose StatementEnd
