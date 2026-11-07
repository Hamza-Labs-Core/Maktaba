-- name: store.open_changes
-- mode: read
-- scope: catalog
-- tables: tv_changes
SELECT * FROM tv_changes WHERE state IN ('draft', 'staged', 'verifying', 'ready') ORDER BY number, created_at
