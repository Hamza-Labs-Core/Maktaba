-- name: review_pages.episodes
-- mode: read
-- scope: library $1
-- tables: series_episodes, series_seasons, videos
SELECT ss.id AS season_id, v.id AS video_id, v.content_hash AS content_hash, COALESCE(v.title, v.filename) AS title, v.duration_sec AS duration, COALESCE(e.episode_override, e.episode) AS episode FROM series_episodes e JOIN series_seasons ss ON ss.series_id = e.series_id  AND ss.season = COALESCE(e.season_override, e.season) JOIN videos v ON v.id = e.video_id WHERE ss.library_id = $1 AND ss.retired_at IS NULL AND v.deleted_at IS NULL ORDER BY ss.id, COALESCE(e.episode_override, e.episode), v.filename
