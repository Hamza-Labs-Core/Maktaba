-- name: sourced_collections.sync_sourced.4
-- mode: write
-- scope: library $2
-- tables: collections*
INSERT INTO collections (id, library_id, name, description, is_smart, origin, auto_rule, source_kind, source_ref, membership_hash, hand_ordered, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,'auto',$6,$7,$8,$9,$10,$11,$11)
