-- name: write_channels.apply_pools.2
-- mode: write
-- scope: catalog
-- tables: filler_pools*
DELETE FROM filler_pools WHERE id = $1 AND external_key IS NOT NULL
