-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0080 (Epic 26 / Story 26.8) — YouTube search cache + import audit.
--
-- `youtube_search_cache` is the rate-limit/TTL cache for the
-- `?include=youtube` search augmentation; only the query string ever
-- egresses (never library contents). `youtube_imports` audits each time
-- a YouTube result's metadata is imported onto a local video — the
-- import itself flows through the same enrichment accept/provenance path
-- (Story 26.6), so user-owned fields stay protected and the action is
-- reversible. The UNIQUE (video_id, youtube_id) makes a re-import an
-- idempotent last-write-wins refresh rather than audit spam.
--
CREATE TABLE IF NOT EXISTS youtube_search_cache (
    query_hash TEXT         PRIMARY KEY,
    response   JSONB        NOT NULL,
    fetched_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ  NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_imports (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id      UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    youtube_id    TEXT NOT NULL,
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    imported_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (video_id, youtube_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS youtube_search_cache_expiry_idx
    ON youtube_search_cache (expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS youtube_search_cache_expiry_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS youtube_imports;
DROP TABLE IF EXISTS youtube_search_cache;
-- +goose StatementEnd
