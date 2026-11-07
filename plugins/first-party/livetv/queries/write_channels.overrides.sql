-- name: write_channels.overrides
-- mode: read
-- scope: catalog
-- tables: channel_art_overrides
SELECT kind, path, settings FROM channel_art_overrides WHERE channel_id = $1
