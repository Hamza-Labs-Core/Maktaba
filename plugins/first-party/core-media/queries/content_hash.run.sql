-- name: content_hash.run
-- mode: write
-- scope: catalog
-- tables: videos*
UPDATE videos SET path = $2, filename = $3 WHERE id = $1
