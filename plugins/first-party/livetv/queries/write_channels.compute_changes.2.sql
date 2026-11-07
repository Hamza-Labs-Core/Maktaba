-- name: write_channels.compute_changes.2
-- mode: read
-- scope: catalog
-- tables: channels
SELECT id, number FROM channels WHERE library_id IS NULL AND lifecycle = 'live'
