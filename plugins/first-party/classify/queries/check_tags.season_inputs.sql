-- name: check_tags.season_inputs
-- mode: read
-- scope: catalog
-- tables: media_metadata_enrichment, series_episodes, series_seasons, videos, youtube_videos
SELECT COALESCE(yv.title, v.title, v.filename) AS title
  FROM series_episodes e
  JOIN series_seasons ss ON ss.series_id = e.series_id
   AND ss.season = COALESCE(e.season_override, e.season)
  JOIN videos v ON v.id = e.video_id AND v.deleted_at IS NULL
  LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
 WHERE ss.id = $1
 ORDER BY COALESCE(e.episode_override, e.episode), v.id
