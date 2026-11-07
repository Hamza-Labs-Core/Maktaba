-- name: common.load_season
-- mode: read
-- scope: catalog
-- tables: series_seasons
SELECT id, series_id, library_id, name, kind, season, membership_hash, retired_at, median_duration_s, metadata FROM series_seasons WHERE id = $1
