-- name: swap.roll_back_swapped
-- mode: write
-- scope: catalog
-- tables: videos*
UPDATE videos SET path = $2, filename = $3, content_hash = $4, size_bytes = $5, mtime = $6 WHERE id = $1
