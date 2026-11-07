-- name: gc.sweep.6
-- mode: read
-- scope: catalog
-- tables: channels
SELECT id FROM channels WHERE lifecycle = 'retired' AND retired_at < $1
