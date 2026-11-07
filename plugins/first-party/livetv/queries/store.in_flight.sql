-- name: store.in_flight
-- mode: read
-- scope: catalog
-- tables: tv_changes
SELECT * FROM tv_changes WHERE state IN ('staged', 'verifying', 'ready', 'approved', 'swapping') ORDER BY created_at LIMIT 1
