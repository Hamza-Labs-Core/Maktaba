-- name: youtube_drain.items
-- mode: read
-- scope: library $1
-- tables: youtube_downloads, youtube_sources
SELECT d.youtube_id AS yid, d.source_id AS sid, d.upload_date AS d, s.folder AS folder, s.options AS options, s.external_id AS ext FROM youtube_downloads d JOIN youtube_sources s ON s.id = d.source_id WHERE d.library_id = $1 AND d.state = 'wanted' AND s.mode IN ('new_and_backfill') AND d.backfill = $2 AND (d.not_before IS NULL OR d.not_before <= $3) ORDER BY (d.upload_date IS NULL), d.upload_date ASC, d.requested_at, d.youtube_id LIMIT $4
