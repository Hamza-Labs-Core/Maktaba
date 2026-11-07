-- name: build.load_release
-- mode: read
-- scope: catalog
-- tables: tv_releases
SELECT * FROM tv_releases WHERE id = $1
