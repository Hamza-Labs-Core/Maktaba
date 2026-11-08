-- name: tag.folder_descriptions
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, videos, youtube_videos
SELECT yv.description AS d FROM videos v JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube' JOIN youtube_videos yv ON yv.youtube_id = m.external_id WHERE v.library_id = $1 AND v.deleted_at IS NULL AND v.path LIKE $2 AND yv.description IS NOT NULL
