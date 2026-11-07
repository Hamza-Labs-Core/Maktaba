-- name: collection.apply
-- mode: write
-- scope: catalog
-- tables: series_episodes, series_seasons, external_links*
DELETE FROM external_links WHERE target = $1 AND subject_kind = 'video' AND subject_id IN (SELECT CAST(e.video_id AS TEXT) FROM series_episodes e  JOIN series_seasons ss ON ss.series_id = e.series_id   AND ss.season = COALESCE(e.season_override, e.season) WHERE ss.id = $2)
