-- name: link.run.2
-- mode: read
-- scope: catalog
-- tables: youtube_videos
SELECT title, upload_date, date_quality FROM youtube_videos WHERE youtube_id = $1
