-- name: youtube_sources.mark_listed
-- mode: write
-- scope: library $1
-- tables: youtube_downloads*
INSERT INTO youtube_downloads (library_id, youtube_id, source_id, state, backfill, reason, upload_date, requested_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (library_id, youtube_id) DO NOTHING
