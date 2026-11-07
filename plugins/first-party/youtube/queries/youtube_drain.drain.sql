-- name: youtube_drain.drain
-- mode: write
-- scope: library $1
-- tables: youtube_downloads*
UPDATE youtube_downloads SET state = 'wanted' WHERE library_id = $1 AND state = 'downloading'
