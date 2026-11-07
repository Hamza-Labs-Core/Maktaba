-- name: copies.promote_copy.4
-- mode: write
-- scope: subject $1
-- tables: videos*
UPDATE videos SET path = $2, filename = $3, size_bytes = $4, missing_confirmed_at = NULL WHERE id = $1
