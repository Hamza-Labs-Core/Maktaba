-- name: repo.live_gen
-- mode: read
-- scope: catalog
-- tables: tv_routing
SELECT live_gen FROM tv_routing WHERE scope = 'cluster'
