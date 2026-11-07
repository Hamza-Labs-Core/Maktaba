-- name: write_channels.actual
-- mode: read
-- scope: catalog
-- tables: channels
SELECT id, number, name, category, mode, mode_config, transition, enabled, sort_order, logo_path FROM channels WHERE id = $1 AND lifecycle = 'live'
