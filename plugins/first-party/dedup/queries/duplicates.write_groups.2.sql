-- name: duplicates.write_groups.2
-- mode: write
-- scope: catalog
-- tables: videos*
UPDATE videos SET full_hash = $2 WHERE id = $1
