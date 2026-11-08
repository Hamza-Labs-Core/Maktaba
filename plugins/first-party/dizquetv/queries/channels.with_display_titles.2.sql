-- name: channels.with_display_titles.2
-- mode: read
-- scope: catalog
-- tables: plex_items, videos
SELECT p.rating_key AS rk, COALESCE(NULLIF(v.display_title_manual, ''), v.display_title) AS t FROM plex_items p JOIN videos v ON v.id = p.video_id WHERE p.rating_key IN (SELECT value FROM json_each($1))
