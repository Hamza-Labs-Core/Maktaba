-- name: write_channels.apply_changes.3
-- mode: write
-- scope: catalog
-- tables: channels*
INSERT INTO channels (id, library_id, number, name, slug, category, mode, mode_config, transition, enabled, sort_order, created_at, updated_at, logo_path) VALUES ($1,NULL,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11,$12)
