-- name: youtube_sources.existing
-- mode: read
-- scope: library $1
-- tables: youtube_downloads
SELECT youtube_id FROM youtube_downloads WHERE library_id = $1 AND youtube_id = ANY($2::text[])
