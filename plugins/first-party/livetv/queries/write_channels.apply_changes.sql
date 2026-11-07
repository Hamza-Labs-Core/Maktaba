-- name: write_channels.apply_changes
-- mode: write
-- scope: catalog
-- tables: channels*
DELETE FROM channels WHERE id = $1
