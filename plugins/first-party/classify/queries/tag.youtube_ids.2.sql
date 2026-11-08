-- name: tag.youtube_ids.2
-- mode: read
-- scope: subject $1
-- tables: media_metadata_enrichment
SELECT external_id FROM media_metadata_enrichment WHERE provider = 'youtube' AND video_id IN (SELECT value FROM json_each($1)) ORDER BY external_id
