-- name: store.create_change.2
-- mode: write
-- scope: catalog
-- tables: tv_changes*
INSERT INTO tv_changes (id, number, state, title, source, change_set, digest, diff, created_by, created_at, updated_at, origin) VALUES ($1, (SELECT COALESCE(MAX(number), 0) + 1 FROM tv_changes),$2,$3,$4,$5,$6,$7,$8,$9,$9,$10)
