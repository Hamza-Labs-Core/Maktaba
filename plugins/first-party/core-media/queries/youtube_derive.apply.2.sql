-- name: youtube_derive.apply.2
-- mode: write
-- scope: youtube_channel $1, library $2
-- tables: youtube_channel_libraries*
INSERT INTO youtube_channel_libraries (channel_id, library_id, linked_videos, updated_at) VALUES ($1, $2, $3, $4) ON CONFLICT (channel_id, library_id) DO UPDATE SET linked_videos = EXCLUDED.linked_videos, updated_at = EXCLUDED.updated_at
