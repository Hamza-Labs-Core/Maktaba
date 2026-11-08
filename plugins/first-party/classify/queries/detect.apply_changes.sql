-- name: detect.apply_changes
-- mode: write
-- scope: library $2
-- tables: series*
INSERT INTO series (id, library_id, name, year, metadata, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$6) ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, year = EXCLUDED.year, metadata = EXCLUDED.metadata, library_id = EXCLUDED.library_id, updated_at = EXCLUDED.updated_at
