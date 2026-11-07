-- name: youtube_sync.store_listing
-- mode: write
-- scope: catalog
-- tables: youtube_videos*
INSERT INTO youtube_videos (youtube_id, channel_id, title, upload_date, date_quality, duration_s, tab, source, fetched_at) VALUES ($1,$2,$3,$4,$5,$6,$7,'youtube',$8) ON CONFLICT (youtube_id) DO UPDATE SET duration_s = COALESCE(youtube_videos.duration_s, EXCLUDED.duration_s), channel_id = COALESCE(youtube_videos.channel_id, EXCLUDED.channel_id), tab = COALESCE(youtube_videos.tab, EXCLUDED.tab), upload_date = CASE WHEN youtube_videos.date_quality IS NULL   THEN EXCLUDED.upload_date ELSE youtube_videos.upload_date END, date_quality = CASE WHEN youtube_videos.date_quality IS NULL   THEN EXCLUDED.date_quality ELSE youtube_videos.date_quality END, title = CASE WHEN youtube_videos.source = 'info_json'   THEN youtube_videos.title ELSE EXCLUDED.title END
