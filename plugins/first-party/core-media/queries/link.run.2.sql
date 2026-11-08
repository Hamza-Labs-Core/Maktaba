-- name: link.run.2
-- mode: read
-- scope: youtube_video $1
-- tables: youtube_videos
SELECT title, upload_date, date_quality FROM youtube_videos WHERE youtube_id = $1
