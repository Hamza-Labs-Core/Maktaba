-- name: clip_groups.input_fingerprint.5
-- mode: read
-- scope: library $1
-- tables: promo_links
SELECT video_id FROM promo_links WHERE library_id = $1 ORDER BY 1
