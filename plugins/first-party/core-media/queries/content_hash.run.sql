-- name: content_hash.run
-- mode: write
-- scope: subject $1
-- tables: videos*
UPDATE videos SET path = $2, filename = $3 WHERE id = $1
