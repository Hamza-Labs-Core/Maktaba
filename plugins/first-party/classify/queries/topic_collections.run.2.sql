-- name: topic_collections.run.2
-- mode: write
-- scope: library $2
-- tables: topic_collections*
INSERT INTO topic_collections (id, library_id, playlist_id, name, membership_hash, updated_at) VALUES ($1,$2,$3,$4,$5,$6)
