-- name: convert_mp4.verify_failed_before
-- mode: read
-- scope: subject $2
-- tables: file_actions
SELECT count(*) FROM file_actions WHERE job_id = $1 AND video_id = $2 AND kind = 'convert' AND state = 'failed' AND verify IS NOT NULL
