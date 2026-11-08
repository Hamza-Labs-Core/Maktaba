-- name: detect.load_current
-- mode: read
-- scope: library $1
-- tables: series_seasons
SELECT id, name, kind, season, membership_hash, retired_at, metadata FROM series_seasons WHERE library_id = $1
