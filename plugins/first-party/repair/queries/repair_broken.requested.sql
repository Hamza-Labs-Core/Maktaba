-- name: repair_broken.requested
-- mode: write
-- scope: subject $1
-- tables: videos*
UPDATE videos SET missing_confirmed_at = NULL WHERE id = $1
