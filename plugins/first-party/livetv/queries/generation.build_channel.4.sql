-- name: generation.build_channel.4
-- mode: write
-- scope: catalog
-- tables: tv_release_channels*
UPDATE tv_release_channels SET boundary_at = $3 WHERE release_id = $1 AND channel_id = $2
