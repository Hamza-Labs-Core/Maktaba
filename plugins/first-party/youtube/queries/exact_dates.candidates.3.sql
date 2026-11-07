-- name: exact_dates.candidates.3
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, series_episodes, videos, youtube_playlist_items, youtube_videos
SELECT yv.youtube_id AS yid, MAX(CASE WHEN se.video_id IS NOT NULL THEN 1 ELSE 0 END) AS in_series, MAX(CASE WHEN pi.youtube_id IS NOT NULL THEN 1 ELSE 0 END) AS in_playlist FROM youtube_videos yv JOIN media_metadata_enrichment m ON m.external_id = yv.youtube_id AND m.provider = 'youtube' AND m.is_dismissed = 0 JOIN videos v ON v.id = m.video_id AND v.library_id = $1 AND v.deleted_at IS NULL LEFT JOIN series_episodes se ON se.video_id = v.id LEFT JOIN youtube_playlist_items pi ON pi.youtube_id = yv.youtube_id WHERE yv.date_quality IS NULL OR yv.date_quality <> 'exact' GROUP BY yv.youtube_id  ORDER BY in_series DESC, yv.youtube_id
