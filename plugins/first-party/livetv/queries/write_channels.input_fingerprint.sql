-- name: write_channels.input_fingerprint
-- mode: read
-- scope: catalog
-- tables: channel_plans
SELECT id, input_hash FROM channel_plans ORDER BY id DESC LIMIT 1
