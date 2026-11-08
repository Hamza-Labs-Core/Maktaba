-- name: dedup_scan.fan_out
-- mode: read
-- scope: library $1
-- tables: videos
SELECT id FROM videos WHERE library_id = $1 AND state NOT IN ('superseded', 'missing') AND superseded_by IS NULL ORDER BY id
