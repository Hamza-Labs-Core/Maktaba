-- name: audit.run_audit
-- mode: read
-- scope: catalog
-- tables: channel_plans
SELECT input_hash FROM channel_plans ORDER BY id DESC LIMIT 1
