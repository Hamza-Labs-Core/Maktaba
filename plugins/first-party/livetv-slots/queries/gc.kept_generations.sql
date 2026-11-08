-- name: gc.kept_generations
-- mode: read
-- scope: catalog
-- tables: tv_routing
SELECT previous_gen, switched_at FROM tv_routing WHERE scope = 'cluster'
