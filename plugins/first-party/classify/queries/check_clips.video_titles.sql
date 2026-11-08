-- name: check_clips.video_titles
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, videos, youtube_videos
SELECT v.id AS vid, COALESCE(yv.title, v.title, v.filename) AS title FROM videos v LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube' LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id WHERE v.library_id = $1 AND v.deleted_at IS NULL
