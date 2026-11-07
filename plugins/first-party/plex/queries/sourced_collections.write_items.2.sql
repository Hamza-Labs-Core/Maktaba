-- name: sourced_collections.write_items.2
-- mode: write
-- scope: subject $2
-- tables: collection_items*
INSERT INTO collection_items (collection_id, video_id, position) VALUES ($1,$2,$3)
