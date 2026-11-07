-- name: build.bootstrap.3
-- mode: write
-- scope: catalog
-- tables: tv_routing*
INSERT INTO tv_routing (scope, live_release_id, live_gen, epoch, switched_at) VALUES ('cluster', $1, $2, 0, $3) ON CONFLICT (scope) DO NOTHING
