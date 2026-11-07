-- name: youtube_sync.unlinked_videos
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, videos
SELECT v.id AS id FROM videos v WHERE v.library_id = $1 AND v.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM media_metadata_enrichment m  WHERE m.video_id = v.id AND m.provider = 'youtube')
