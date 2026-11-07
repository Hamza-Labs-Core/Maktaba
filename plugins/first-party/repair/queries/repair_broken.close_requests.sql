-- name: repair_broken.close_requests
-- mode: write
-- scope: subject $1
-- tables: item_requests*
UPDATE item_requests SET done_at = $2, result = $3 WHERE video_id = $1 AND action = 'redownload' AND done_at IS NULL
