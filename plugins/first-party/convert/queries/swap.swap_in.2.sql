-- name: swap.swap_in.2
-- mode: write
-- scope: catalog
-- tables: videos*
UPDATE videos SET path = $2, filename = $3, content_hash = $4, size_bytes = $5, mtime = $6, updated_at = $7 WHERE id = $1
