-- name: tag.drop_departed_episode_tags
-- mode: write
-- scope: library $1
-- tables: series_episodes, series_seasons, videos, episode_tags*
DELETE FROM episode_tags WHERE video_id IN (
  SELECT v.id FROM videos v
   WHERE v.library_id = $1
     AND NOT EXISTS (
       SELECT 1 FROM series_episodes e
         JOIN series_seasons ss ON ss.series_id = e.series_id
          AND ss.season = COALESCE(e.season_override, e.season)
        WHERE e.video_id = v.id AND ss.retired_at IS NULL))
