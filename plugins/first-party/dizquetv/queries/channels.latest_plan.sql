-- name: channels.latest_plan
-- mode: read
-- scope: catalog
-- tables: channel_plans
SELECT input_hash, plan FROM channel_plans ORDER BY id DESC LIMIT 1
