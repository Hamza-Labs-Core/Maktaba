-- name: build.bootstrap.2
-- mode: read
-- scope: catalog
-- tables: tv_releases
SELECT id FROM tv_releases WHERE sched_gen = $1
