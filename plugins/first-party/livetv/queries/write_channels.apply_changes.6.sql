-- name: write_channels.apply_changes.6
-- mode: read
-- scope: catalog
-- tables: channels
SELECT 1 AS x FROM channels WHERE id = $1
