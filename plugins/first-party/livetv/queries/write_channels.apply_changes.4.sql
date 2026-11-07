-- name: write_channels.apply_changes.4
-- mode: write
-- scope: catalog
-- tables: channels*
UPDATE channels SET number = $2, name = $3, category = $4, mode = $5, mode_config = $6, transition = $7, enabled = $8, sort_order = $9, updated_at = $10, logo_path = $11 WHERE id = $1
