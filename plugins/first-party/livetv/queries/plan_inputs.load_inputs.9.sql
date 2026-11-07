-- name: plan_inputs.load_inputs.9
-- mode: read
-- scope: catalog
-- tables: media_metadata_enrichment, series_episodes, videos, youtube_channels, youtube_videos
SELECT v.id AS vid, v.path AS path, COALESCE(yv.title, v.title, v.filename) AS title,
       COALESCE(v.duration_sec, yv.duration_s) AS dur, yv.upload_date AS upload_date,
       yv.tab AS tab, v.metadata AS metadata, yc.title AS channel
  FROM videos v
  LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
  LEFT JOIN youtube_channels yc ON yc.channel_id = yv.channel_id
 WHERE v.deleted_at IS NULL
   AND NOT EXISTS (SELECT 1 FROM series_episodes e WHERE e.video_id = v.id)
 ORDER BY v.id
