-- name: detect.input_fingerprint.4
-- mode: read
-- scope: library $1
-- tables: series_episodes, videos
SELECT e.video_id AS vid, e.season_override AS so, e.episode_override AS eo FROM series_episodes e JOIN videos v ON v.id = e.video_id WHERE v.library_id = $1 AND (e.season_override IS NOT NULL OR e.episode_override IS NOT NULL) ORDER BY e.video_id
