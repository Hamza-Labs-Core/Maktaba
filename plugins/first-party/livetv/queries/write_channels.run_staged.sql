-- name: write_channels.run_staged
-- mode: read
-- scope: catalog
-- tables: channel_plans
SELECT id FROM channel_plans ORDER BY id DESC LIMIT 1
