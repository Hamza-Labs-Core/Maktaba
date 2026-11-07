-- name: store.create_change
-- mode: write
-- scope: catalog
-- tables: tv_changes*
INSERT INTO tv_changes (id, state, title, source, change_set, digest, diff, created_by, created_at, updated_at, origin) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,$10)
