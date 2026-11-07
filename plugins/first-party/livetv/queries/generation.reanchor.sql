-- name: generation.reanchor
-- mode: read
-- scope: catalog
-- tables: tv_release_channels
SELECT channel_id, boundary_at FROM tv_release_channels WHERE release_id = $1 AND sched_changed = $2
