-- name: repo.read_lanes.2
-- mode: read
-- scope: catalog
-- tables: channel_plans
SELECT json_extract(plan, $1) AS lane FROM channel_plans ORDER BY id DESC LIMIT 1
