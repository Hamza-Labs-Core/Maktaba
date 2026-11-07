-- name: collections_import.plan_plex_collections
-- mode: read
-- scope: library $2
-- tables: plex_items, videos
SELECT p.rating_key AS rk, p.video_id AS vid FROM plex_items p JOIN videos v ON v.id = p.video_id WHERE p.server = $1 AND p.library_id = $2 AND v.deleted_at IS NULL
