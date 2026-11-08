-- name: audit.store
-- mode: write
-- scope: catalog
-- tables: channel_audits*
INSERT INTO channel_audits (created_at, plan_hash, window_days, channels, html_path) VALUES ($1,$2,$3,$4,$5) RETURNING id
