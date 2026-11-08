-- name: check_clips.load_clips
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, series_episodes, video_topic_tags, videos, youtube_videos
SELECT v.id AS vid, COALESCE(yv.title, v.title, v.filename) AS title
  FROM videos v
  LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
 WHERE v.library_id = $1 AND v.deleted_at IS NULL
   AND COALESCE(v.duration_sec, yv.duration_s) <= $2
   AND NOT EXISTS (SELECT 1 FROM series_episodes e WHERE e.video_id = v.id)
   AND EXISTS (SELECT 1 FROM video_topic_tags t WHERE t.video_id = v.id
               AND t.tag LIKE 'topic:%' AND t.score >= 0.5)
 ORDER BY v.path
