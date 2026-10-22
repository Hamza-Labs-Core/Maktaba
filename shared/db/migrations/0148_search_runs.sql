-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0148 (search results per video) - a user's searches, their saved result sets
-- and the Recent searches list.
--
-- POST /api/search computes the whole ordered result of a search once (the title
-- matches, and the videos whose transcripts match grouped per video with their true
-- match counts and the meaning search's passages) and keeps it here under an opaque,
-- unguessable id (`id`, the `search_id`). Paging either list, a video's passages and
-- its matches page read that snapshot, so the order and the counts never shift while
-- the user pages through them or comes back from a video.
--
--   result           the ordered set (JSON text); NULL once it is no longer kept: the
--                    row stays as a Recent search (the query, mode and counts)
--   expires_at_ms    sliding: every read moves it to now + rules.search.result_minutes;
--                    Unix milliseconds, so SQLite and Postgres compare it the same way
--   last_used_at_ms  the last read
--
-- Bounded: a new search empties the results of every expired row, replaces the user's
-- older run of the same query, mode and filters, and keeps the user's newest
-- rules.search.recent_limit rows. Only the home API reads and writes it (search is a
-- home route): nothing for maktaba_worker. Additive.
--
CREATE TABLE IF NOT EXISTS search_runs (
    id               TEXT        PRIMARY KEY,
    user_id          UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    query            TEXT        NOT NULL,
    mode             TEXT        NOT NULL,
    filters          TEXT        NOT NULL DEFAULT '{}',
    title_count      INTEGER     NOT NULL DEFAULT 0,
    transcript_count INTEGER     NOT NULL DEFAULT 0,
    degraded         BOOLEAN     NOT NULL DEFAULT false,
    result           TEXT,
    created_at_ms    BIGINT      NOT NULL,
    last_used_at_ms  BIGINT      NOT NULL,
    expires_at_ms    BIGINT      NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS search_runs_user_recent_idx
    ON search_runs (user_id, created_at_ms DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS search_runs_kept_idx
    ON search_runs (expires_at_ms) WHERE result IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- slot 0103's default privileges gave the group role rights on new tables: the
        -- home API is the only reader and writer of a user's searches.
        REVOKE ALL ON search_runs FROM maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS search_runs;
-- +goose StatementEnd
