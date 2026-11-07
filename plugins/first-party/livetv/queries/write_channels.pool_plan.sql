-- name: write_channels.pool_plan
-- mode: read
-- scope: catalog
-- tables: filler_pools
SELECT id, external_key, name FROM filler_pools WHERE external_key IS NOT NULL
