-- name: index.run_index
-- mode: write
-- scope: catalog
-- tables: videos*
UPDATE videos SET duration_sec = $2 WHERE id = $1 AND duration_sec IS NULL
