-- name: youtube_drain.drain.2
-- mode: write
-- scope: catalog
-- tables: youtube_sources*
UPDATE youtube_sources SET last_fetched_at = $2, status = 'ok', error = NULL WHERE id = $1
