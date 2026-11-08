-- name: content_hash.run.3
-- mode: write
-- scope: subject $1
-- tables: videos*
UPDATE videos SET metadata = $2, size_bytes = $3 WHERE id = $1
