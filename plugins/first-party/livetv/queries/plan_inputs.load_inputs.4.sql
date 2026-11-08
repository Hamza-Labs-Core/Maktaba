-- name: plan_inputs.load_inputs.4
-- mode: read
-- scope: catalog
-- tables: media_metadata_enrichment, series_episodes, series_seasons, videos, youtube_channels, youtube_videos
SELECT ss.id AS season_id, v.id AS vid, v.path AS path,
       COALESCE(yv.title, v.title, v.filename) AS title,
       COALESCE(v.duration_sec, yv.duration_s) AS dur, yv.upload_date AS upload_date,
       yv.date_quality AS date_quality,
       e.season AS season, e.episode AS episode, yc.title AS channel
  FROM series_episodes e
  JOIN series_seasons ss ON ss.series_id = e.series_id
       AND ss.season = COALESCE(e.season_override, e.season) AND ss.retired_at IS NULL
  JOIN videos v ON v.id = e.video_id AND v.deleted_at IS NULL
  LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
  LEFT JOIN youtube_channels yc ON yc.channel_id = yv.channel_id
 ORDER BY ss.id, COALESCE(e.episode_override, e.episode), v.id
