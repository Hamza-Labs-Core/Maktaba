-- name: youtube_sources.load_sources.2
-- mode: read
-- scope: library $1
-- tables: youtube_sources
SELECT id, library_id, kind, url, external_id, folder, mode, added_at, options, status, last_checked_at FROM youtube_sources WHERE library_id = $1 ORDER BY kind, external_id
