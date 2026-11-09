-- name: convert.convert_settings
-- mode: read
-- scope: library $1
-- tables: libraries
SELECT settings FROM libraries WHERE id = $1
