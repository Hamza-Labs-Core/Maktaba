-- name: display.display_info.2
-- mode: read
-- scope: subject $1
-- tables: series, series_episodes, videos
SELECT v.id AS id, COALESCE(NULLIF(v.display_title_manual, ''), v.display_title) AS title, (v.display_title_manual IS NOT NULL AND v.display_title_manual <> '') AS manual, v.display_title IS NOT NULL AS done, COALESCE(s.display_name, s.name_override, s.name) AS series, COALESCE(se.episode_override, se.episode, se.absolute_number) AS ep, se.series_id AS sid FROM videos v LEFT JOIN series_episodes se ON se.video_id = v.id LEFT JOIN series s ON s.id = se.series_id WHERE v.id IN (SELECT value FROM json_each($1))
