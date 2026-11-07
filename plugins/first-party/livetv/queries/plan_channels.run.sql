-- name: plan_channels.run
-- mode: read
-- scope: catalog
-- tables: channel_plans
SELECT plan, input_hash FROM channel_plans ORDER BY id DESC LIMIT 1
