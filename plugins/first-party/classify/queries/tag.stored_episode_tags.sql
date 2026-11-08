-- name: tag.stored_episode_tags
-- mode: read
-- scope: catalog
-- tables: episode_tags, series_episodes, series_seasons
SELECT t.video_id AS vid, t.tag AS tag, t.score AS score FROM episode_tags t WHERE t.video_id IN (SELECT e.video_id FROM series_episodes e JOIN series_seasons ss ON ss.series_id = e.series_id  AND ss.season = COALESCE(e.season_override, e.season) WHERE ss.id = $1)
