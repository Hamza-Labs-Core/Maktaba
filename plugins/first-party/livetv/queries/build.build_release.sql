-- name: build.build_release
-- mode: read
-- scope: catalog
-- tables: tv_releases
SELECT id FROM tv_releases WHERE digest = $1
