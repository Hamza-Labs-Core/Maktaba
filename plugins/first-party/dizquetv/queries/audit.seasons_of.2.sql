-- name: audit.seasons_of.2
-- mode: read
-- scope: catalog
-- tables: series_episodes, series_seasons
SELECT e.video_id AS vid, ss.id AS sid, ss.library_id AS lib FROM series_episodes e JOIN series_seasons ss ON ss.series_id = e.series_id  AND ss.season = COALESCE(e.season_override, e.season) WHERE ss.retired_at IS NULL AND e.video_id IN (SELECT value FROM json_each($1))
