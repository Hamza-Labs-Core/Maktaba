-- name: convert.convert_settings
-- mode: read
-- scope: catalog
-- tables: libraries
SELECT settings FROM libraries WHERE id = $1
