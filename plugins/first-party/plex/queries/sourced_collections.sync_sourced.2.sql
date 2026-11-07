-- name: sourced_collections.sync_sourced.2
-- mode: read
-- scope: library $1
-- tables: collections
SELECT id, source_kind, source_ref, membership_hash FROM collections WHERE library_id = $1 AND source_kind IN (SELECT value FROM json_each($2))
