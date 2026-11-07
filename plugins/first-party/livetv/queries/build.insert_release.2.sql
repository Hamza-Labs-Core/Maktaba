-- name: build.insert_release.2
-- mode: write
-- scope: catalog
-- tables: tv_release_channels*
INSERT INTO tv_release_channels (release_id, channel_id, def, art, sched_changed) VALUES ($1,$2,$3,$4,$5)
