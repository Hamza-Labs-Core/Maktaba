-- name: detect.load_current.2
-- mode: read
-- scope: library $1
-- tables: series_episodes, series_seasons
SELECT ss.id AS sid, e.video_id AS vid FROM series_episodes e JOIN series_seasons ss ON ss.series_id = e.series_id AND ss.season = e.season WHERE ss.library_id = $1 ORDER BY ss.id, e.episode, e.video_id
