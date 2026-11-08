-- name: write_channels.apply_pools.3
-- mode: write
-- scope: catalog
-- tables: filler_pools*
INSERT INTO filler_pools (id, channel_id, name, external_key, created_at, updated_at) VALUES ($1, NULL, $2, $3, $4, $4)
