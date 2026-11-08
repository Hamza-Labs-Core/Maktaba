-- name: detect.apply_changes.3
-- mode: read
-- scope: library $1
-- tables: series_episodes, videos
SELECT e.video_id AS video_id FROM series_episodes e JOIN videos v ON v.id = e.video_id WHERE v.library_id = $1 AND e.season_override IS NULL AND e.episode_override IS NULL
