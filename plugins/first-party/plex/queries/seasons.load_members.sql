-- name: seasons.load_members
-- mode: read
-- scope: catalog
-- tables: series_episodes, series_seasons, videos
SELECT v.id AS id, v.path AS path, COALESCE(v.title, v.filename) AS title FROM series_episodes e JOIN series_seasons ss ON ss.series_id = e.series_id  AND ss.season = COALESCE(e.season_override, e.season) JOIN videos v ON v.id = e.video_id WHERE ss.id = $1 AND v.deleted_at IS NULL ORDER BY (COALESCE(e.episode_override, e.episode) IS NULL), COALESCE(e.episode_override, e.episode), e.absolute_number, v.id
