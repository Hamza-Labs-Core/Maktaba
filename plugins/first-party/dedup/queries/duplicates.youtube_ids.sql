-- name: duplicates.youtube_ids
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, videos
SELECT DISTINCT e.external_id AS yt, e.video_id AS video_id FROM media_metadata_enrichment e JOIN videos v ON v.id = e.video_id WHERE v.library_id = $1 AND e.provider = 'youtube' AND NOT COALESCE(e.is_dismissed, false)
