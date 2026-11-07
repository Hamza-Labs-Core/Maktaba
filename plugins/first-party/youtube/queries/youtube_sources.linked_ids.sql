-- name: youtube_sources.linked_ids
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, videos
SELECT DISTINCT m.external_id AS y FROM media_metadata_enrichment m JOIN videos v ON v.id = m.video_id WHERE v.library_id = $1 AND v.deleted_at IS NULL AND m.provider = 'youtube' AND m.external_id IS NOT NULL
