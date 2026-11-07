-- name: swap.swap_in
-- mode: read
-- scope: catalog
-- tables: videos
SELECT id, library_id, path, content_hash, size_bytes FROM videos WHERE id = $1
