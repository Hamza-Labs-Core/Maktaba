-- name: stage.stage_change.2
-- mode: read
-- scope: catalog
-- tables: tv_routing
SELECT live_release_id FROM tv_routing WHERE scope = 'cluster'
