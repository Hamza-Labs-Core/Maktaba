-- name: plex_items.items_for_videos.2
-- mode: read
-- scope: catalog
-- tables: plex_items
SELECT rating_key, section_key, file, duration_ms, video_id FROM plex_items WHERE video_id IN (SELECT value FROM json_each($1))
