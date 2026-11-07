-- name: youtube_drain.backfill_progress
-- mode: read
-- scope: library $1
-- tables: youtube_downloads, youtube_sources
SELECT count(*) AS total, sum(CASE WHEN d.state = 'wanted' THEN 0 ELSE 1 END) AS done FROM youtube_downloads d JOIN youtube_sources s ON s.id = d.source_id WHERE d.library_id = $1 AND d.backfill = $2 AND s.mode = 'new_and_backfill'
