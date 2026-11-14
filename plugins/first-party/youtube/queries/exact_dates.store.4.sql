-- name: exact_dates.store.4
-- mode: write
-- scope: youtube_video $1
-- tables: youtube_videos*
UPDATE youtube_videos SET license = $2 WHERE youtube_id = $1
