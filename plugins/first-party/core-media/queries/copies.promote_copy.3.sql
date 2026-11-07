-- name: copies.promote_copy.3
-- mode: write
-- scope: catalog
-- tables: videos*
UPDATE videos SET path = $2, filename = $3, size_bytes = $4, missing_confirmed_at = NULL, mtime = $5 WHERE id = $1
