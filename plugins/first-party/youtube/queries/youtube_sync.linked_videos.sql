-- name: youtube_sync.linked_videos
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, videos
SELECT m.video_id AS id FROM media_metadata_enrichment m JOIN videos v ON v.id = m.video_id WHERE v.library_id = $1 AND v.deleted_at IS NULL AND m.provider = 'youtube' AND m.external_id = ANY($2::text[])
