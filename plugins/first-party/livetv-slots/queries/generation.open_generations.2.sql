-- name: generation.open_generations.2
-- mode: read
-- scope: catalog
-- tables: tv_routing
SELECT previous_release_id, previous_gen, previous_until FROM tv_routing WHERE scope = 'cluster'
