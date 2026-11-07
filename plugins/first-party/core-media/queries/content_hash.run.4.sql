-- name: content_hash.run.4
-- mode: write
-- scope: subject $1
-- tables: videos*
UPDATE videos SET content_hash = $2, size_bytes = $3, metadata = $4 WHERE id = $1
