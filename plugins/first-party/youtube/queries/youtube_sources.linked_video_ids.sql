-- name: youtube_sources.linked_video_ids
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, videos
SELECT m.external_id AS y, m.video_id AS v FROM media_metadata_enrichment m JOIN videos v ON v.id = m.video_id WHERE v.library_id = $1 AND v.deleted_at IS NULL AND m.provider = 'youtube' AND m.external_id IS NOT NULL
