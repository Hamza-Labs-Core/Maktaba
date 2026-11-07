-- name: clip_groups.plan_library_groups.2
-- mode: read
-- scope: library $1
-- tables: promo_links
SELECT video_id FROM promo_links WHERE library_id = $1
