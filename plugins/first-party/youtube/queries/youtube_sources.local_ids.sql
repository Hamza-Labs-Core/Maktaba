-- name: youtube_sources.local_ids
-- mode: read
-- scope: library $1
-- tables: youtube_downloads
SELECT youtube_id AS y FROM youtube_downloads WHERE library_id = $1 AND state = 'done'
