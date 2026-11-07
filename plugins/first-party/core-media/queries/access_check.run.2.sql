-- name: access_check.run.2
-- mode: write
-- scope: subject $1
-- tables: videos*
UPDATE videos SET missing_confirmed_at = NULL WHERE id = $1
