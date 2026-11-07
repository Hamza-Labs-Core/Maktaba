-- name: build.load_release.2
-- mode: read
-- scope: catalog
-- tables: tv_release_channels
SELECT channel_id, def, art, sched_changed FROM tv_release_channels WHERE release_id = $1
