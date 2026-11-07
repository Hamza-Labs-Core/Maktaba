-- name: gc.sweep.9
-- mode: read
-- scope: catalog
-- tables: channels
SELECT id FROM channels WHERE lifecycle = 'staged'
