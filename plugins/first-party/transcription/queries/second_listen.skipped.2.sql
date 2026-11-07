-- name: second_listen.skipped.2
-- mode: write
-- scope: subject $1
-- tables: relisten_attempts*
DELETE FROM relisten_attempts WHERE video_id = $1
