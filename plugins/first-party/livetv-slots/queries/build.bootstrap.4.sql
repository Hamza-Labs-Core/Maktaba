-- name: build.bootstrap.4
-- mode: write
-- scope: catalog
-- tables: tv_routing*
UPDATE tv_routing SET live_release_id = $1 WHERE scope = 'cluster' AND live_release_id IS NULL
