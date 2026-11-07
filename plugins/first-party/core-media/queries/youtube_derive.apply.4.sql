-- name: youtube_derive.apply.4
-- mode: write
-- scope: library $1
-- tables: youtube_sources*
INSERT INTO youtube_sources (library_id, kind, url, external_id, mode, added_at, derived_at) VALUES ($1, $2, $3, $4, 'off', $5, $5) ON CONFLICT (library_id, kind, external_id) DO NOTHING RETURNING id
