-- name: leftovers.load_leftovers.4
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, videos, youtube_videos
SELECT v.id AS vid, COALESCE(yv.title, v.title, v.filename) AS title,
       COALESCE(v.duration_sec, yv.duration_s) AS dur, yv.upload_date AS upload_date,
       yv.tab AS tab, v.metadata AS metadata
  FROM videos v
  LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
 WHERE v.library_id = $1 AND v.deleted_at IS NULL
 ORDER BY v.id
