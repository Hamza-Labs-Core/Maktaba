-- name: tag.input_fingerprint
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, series_episodes, videos, youtube_videos
SELECT v.id AS id, v.path AS path, COALESCE(yv.title, v.title, v.filename) AS title,
       yv.description AS description, COALESCE(v.duration_sec, yv.duration_s) AS dur,
       yv.tab AS tab, v.metadata AS metadata
  FROM videos v
  LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
 WHERE v.library_id = $1 AND v.deleted_at IS NULL
   AND NOT EXISTS (SELECT 1 FROM series_episodes e WHERE e.video_id = v.id)
 ORDER BY v.id
