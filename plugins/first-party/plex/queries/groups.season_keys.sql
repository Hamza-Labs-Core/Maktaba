-- name: groups.season_keys
-- mode: read
-- scope: library $1
-- tables: external_links, series, series_episodes
SELECT l.external_id AS rk FROM external_links l JOIN series_episodes e ON CAST(e.video_id AS TEXT) = l.subject_id JOIN series s ON s.id = e.series_id WHERE l.target = 'plex_item' AND l.subject_kind = 'video' AND s.library_id = $1
