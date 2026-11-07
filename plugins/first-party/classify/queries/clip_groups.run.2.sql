-- name: clip_groups.run.2
-- mode: write
-- scope: library $2
-- tables: clip_groups*
INSERT INTO clip_groups (id, library_id, kind, basis, name, topic, title, membership_hash, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
