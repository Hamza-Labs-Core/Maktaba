-- name: tag.youtube_ids
-- mode: read
-- scope: catalog
-- tables: media_metadata_enrichment
SELECT external_id FROM media_metadata_enrichment WHERE provider = 'youtube' AND video_id = ANY($1::uuid[]) ORDER BY external_id
