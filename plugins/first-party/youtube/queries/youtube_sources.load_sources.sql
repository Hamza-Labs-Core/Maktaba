-- name: youtube_sources.load_sources
-- mode: read
-- scope: library $1
-- tables: youtube_sources
SELECT id, library_id, kind, url, external_id, folder, mode, added_at, options, status, last_checked_at FROM youtube_sources WHERE library_id = $1 AND mode <> 'off' ORDER BY kind, external_id
