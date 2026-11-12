-- name: link.upsert_youtube_video.4
-- mode: write
-- scope: youtube_video $1, youtube_channel $2
-- tables: youtube_videos*
INSERT INTO youtube_videos (youtube_id, channel_id, title, upload_date, date_quality, duration_s, description, source, fetched_at, age_limit) VALUES ($1,$2,$3,$4,$5,$6,$7,'info_json',$8,$9) ON CONFLICT (youtube_id) DO UPDATE SET title = EXCLUDED.title, age_limit = COALESCE(EXCLUDED.age_limit, youtube_videos.age_limit), upload_date = COALESCE(EXCLUDED.upload_date, youtube_videos.upload_date), date_quality = CASE WHEN EXCLUDED.upload_date IS NOT NULL THEN 'exact'   ELSE youtube_videos.date_quality END, duration_s = COALESCE(EXCLUDED.duration_s, youtube_videos.duration_s), channel_id = COALESCE(EXCLUDED.channel_id, youtube_videos.channel_id), description = COALESCE(EXCLUDED.description, youtube_videos.description)
