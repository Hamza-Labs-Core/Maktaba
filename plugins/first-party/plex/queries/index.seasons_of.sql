-- name: index.seasons_of
-- mode: read
-- scope: catalog
-- tables: series_episodes, series_seasons
SELECT DISTINCT ss.id AS id FROM series_episodes e JOIN series_seasons ss ON ss.series_id = e.series_id  AND ss.season = COALESCE(e.season_override, e.season) WHERE e.video_id = ANY($1::uuid[])
