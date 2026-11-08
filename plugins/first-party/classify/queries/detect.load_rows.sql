-- name: detect.load_rows
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, video_signals, videos, youtube_channels, youtube_videos
SELECT v.id AS id, v.path AS path, v.filename AS filename, v.title AS title,
       v.duration_sec AS duration_sec, v.metadata AS metadata,
       vs.thumb_dhash AS thumb_dhash, vs.audio_head AS audio_head, vs.audio_tail AS audio_tail,
       vs.frame_hashes AS frame_hashes, vs.fp_version AS fp_version, vs.fp_partial AS fp_partial,
       m.external_id AS youtube_id,
       yv.title AS yt_title, yv.upload_date AS upload_date, yv.date_quality AS date_quality,
       yv.duration_s AS yt_duration, yv.tab AS tab, yc.title AS channel_title
  FROM videos v
  LEFT JOIN video_signals vs ON vs.video_id = v.id
  LEFT JOIN media_metadata_enrichment m
         ON m.video_id = v.id AND m.provider = 'youtube' AND NOT m.is_dismissed
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
  LEFT JOIN youtube_channels yc ON yc.channel_id = yv.channel_id
 WHERE v.library_id = $1 AND v.deleted_at IS NULL
 ORDER BY v.id
