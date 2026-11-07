-- name: sourced_collections.sync_sourced.5
-- mode: write
-- scope: catalog
-- tables: collections*
UPDATE collections SET name = $2, description = $3, auto_rule = $4, membership_hash = $5, hand_ordered = $6, updated_at = $7 WHERE id = $1
