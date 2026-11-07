-- name: youtube_sources.seed_from_library
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, videos, youtube_downloads
SELECT m.external_id AS y, m.video_id AS v FROM media_metadata_enrichment m JOIN videos v ON v.id = m.video_id WHERE v.library_id = $1 AND v.deleted_at IS NULL AND m.provider = 'youtube' AND m.external_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM youtube_downloads d WHERE d.library_id = v.library_id   AND d.youtube_id = m.external_id AND d.state = 'done')
