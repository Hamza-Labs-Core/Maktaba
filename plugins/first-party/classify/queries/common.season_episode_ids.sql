-- name: common.season_episode_ids
-- mode: read
-- scope: catalog
-- tables: series_episodes, series_seasons
SELECT e.video_id AS id FROM series_episodes e JOIN series_seasons ss ON ss.series_id = e.series_id  AND ss.season = COALESCE(e.season_override, e.season) WHERE ss.id = $1 ORDER BY COALESCE(e.episode_override, e.episode), e.video_id
