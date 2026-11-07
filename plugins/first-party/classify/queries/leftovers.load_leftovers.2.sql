-- name: leftovers.load_leftovers.2
-- mode: read
-- scope: library $1
-- tables: series_episodes, series_seasons
SELECT e.video_id AS vid, ss.kind AS kind FROM series_episodes e JOIN series_seasons ss ON ss.series_id = e.series_id  AND ss.season = COALESCE(e.season_override, e.season) AND ss.retired_at IS NULL WHERE ss.library_id = $1
