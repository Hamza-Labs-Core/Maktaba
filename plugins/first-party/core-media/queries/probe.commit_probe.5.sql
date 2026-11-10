-- name: probe.commit_probe.5
-- mode: write
-- scope: subject $1
-- tables: videos*
UPDATE videos SET duration_sec = $2 WHERE id = $1
