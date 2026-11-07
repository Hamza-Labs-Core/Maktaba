-- name: build.build_release.3
-- mode: write
-- scope: library $2
-- tables: channels*
INSERT INTO channels (id, library_id, number, name, slug, logo_path, category, mode, mode_config, source_filter, transition, enabled, sort_order, created_at, updated_at, lifecycle) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14, 'staged')
