-- +goose NO TRANSACTION
-- +goose Up
-- Search matches video titles (title ILIKE '%q%', ordered by similarity()):
-- a fresh library has titles long before it has transcripts. A trigram index
-- serves both on 150k titles; CONCURRENTLY so a live library keeps writing.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_title_trgm_idx
    ON videos USING gin (title gin_trgm_ops);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS videos_title_trgm_idx;
