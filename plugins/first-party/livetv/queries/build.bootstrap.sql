-- name: build.bootstrap
-- mode: read
-- scope: catalog
-- tables: tv_routing
SELECT live_release_id, live_gen FROM tv_routing WHERE scope = 'cluster'
