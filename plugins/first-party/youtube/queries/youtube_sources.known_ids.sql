-- name: youtube_sources.known_ids
-- mode: read
-- scope: library $1
-- tables: youtube_downloads
SELECT youtube_id AS y FROM youtube_downloads WHERE library_id = $1
