-- name: sourced_collections.sync_sourced
-- mode: read
-- scope: library $1
-- tables: collections
SELECT id, source_kind, source_ref, membership_hash FROM collections WHERE library_id = $1 AND source_kind = ANY($2::text[])
