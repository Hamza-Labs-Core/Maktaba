-- name: collections_import.run_collections_import.2
-- mode: read
-- scope: catalog
-- tables: external_links
SELECT external_id FROM external_links WHERE target IN ($1, $2, $3)
