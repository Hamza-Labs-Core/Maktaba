-- name: repo.filler_pools
-- mode: read
-- scope: catalog
-- tables: filler_pools
SELECT id, external_key, channel_id FROM filler_pools WHERE external_key IS NOT NULL OR channel_id IS NOT NULL
