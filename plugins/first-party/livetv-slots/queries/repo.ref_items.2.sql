-- name: repo.ref_items.2
-- mode: read
-- scope: catalog
-- tables: series_episodes, videos
SELECT v.id, v.title, v.duration_sec FROM series_episodes se JOIN videos v ON v.id = se.video_id WHERE se.series_id = $1 AND v.deleted_at IS NULL AND v.duration_sec IS NOT NULL AND v.duration_sec > 0 ORDER BY COALESCE(se.season_override, se.season, 0), COALESCE(se.episode_override, se.episode, se.absolute_number, 0), v.title
