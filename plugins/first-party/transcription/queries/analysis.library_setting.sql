-- name: analysis.library_setting
-- mode: read
-- scope: catalog
-- tables: libraries
SELECT settings FROM libraries WHERE id = $1
