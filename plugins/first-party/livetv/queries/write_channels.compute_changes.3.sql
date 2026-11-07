-- name: write_channels.compute_changes.3
-- mode: read
-- scope: catalog
-- tables: channels
SELECT id, slug FROM channels WHERE library_id IS NULL AND lifecycle = 'live'
