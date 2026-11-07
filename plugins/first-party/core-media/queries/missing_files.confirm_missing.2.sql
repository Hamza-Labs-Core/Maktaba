-- name: missing_files.confirm_missing.2
-- mode: write
-- scope: catalog
-- tables: videos*
UPDATE videos SET missing_confirmed_at = $2 WHERE id = $1 AND missing_confirmed_at IS NULL
