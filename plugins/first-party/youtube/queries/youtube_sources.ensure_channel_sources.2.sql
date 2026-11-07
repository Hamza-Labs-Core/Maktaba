-- name: youtube_sources.ensure_channel_sources.2
-- mode: write
-- scope: library $1
-- tables: youtube_sources*
INSERT INTO youtube_sources (library_id, kind, url, external_id, mode, added_at) VALUES ($1, 'channel', $2, $3, 'off', $4) ON CONFLICT (library_id, kind, external_id) DO NOTHING
