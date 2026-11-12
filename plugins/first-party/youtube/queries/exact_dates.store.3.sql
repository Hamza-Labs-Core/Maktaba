-- name: exact_dates.store.3
-- mode: write
-- scope: youtube_video $1
-- tables: youtube_videos*
UPDATE youtube_videos SET age_limit = $2 WHERE youtube_id = $1
