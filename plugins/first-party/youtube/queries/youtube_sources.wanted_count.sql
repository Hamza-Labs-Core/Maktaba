-- name: youtube_sources.wanted_count
-- mode: read
-- scope: library $1
-- tables: youtube_downloads, youtube_sources
SELECT count(*) FROM youtube_downloads d JOIN youtube_sources s ON s.id = d.source_id WHERE d.library_id = $1 AND d.state = 'wanted' AND s.mode IN ('new_and_backfill') AND d.backfill = $2 AND (d.not_before IS NULL OR d.not_before <= $3)
