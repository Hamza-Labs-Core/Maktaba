-- name: trash.purge_due
-- mode: write
-- scope: subject $1
-- tables: videos*
DELETE FROM videos WHERE id = $1 AND state = 'superseded' RETURNING id
