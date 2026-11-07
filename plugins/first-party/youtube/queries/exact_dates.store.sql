-- name: exact_dates.store
-- mode: write
-- scope: catalog
-- tables: youtube_videos*
UPDATE youtube_videos SET upload_date = $2, date_quality = 'exact', description = COALESCE(description, $3) WHERE youtube_id = $1
