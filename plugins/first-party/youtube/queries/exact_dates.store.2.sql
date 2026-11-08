-- name: exact_dates.store.2
-- mode: write
-- scope: youtube_video $1
-- tables: youtube_videos*
UPDATE youtube_videos SET description = COALESCE(description, $2) WHERE youtube_id = $1
