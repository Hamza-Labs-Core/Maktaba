-- name: build.build_release.2
-- mode: read
-- scope: catalog
-- tables: channels
SELECT 1 AS x FROM channels WHERE id = $1
