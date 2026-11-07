-- name: topic_collections.stored_series.2
-- mode: read
-- scope: library $1
-- tables: series, series_episodes
SELECT e.series_id AS series_id, COALESCE(e.season_override, e.season) AS season, e.video_id AS vid FROM series_episodes e JOIN series s ON s.id = e.series_id WHERE s.library_id = $1 ORDER BY e.series_id, COALESCE(e.season_override, e.season), COALESCE(e.episode_override, e.episode), e.video_id
