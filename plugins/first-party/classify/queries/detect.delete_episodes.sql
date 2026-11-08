-- name: detect.delete_episodes
-- mode: write
-- scope: subject $1
-- tables: series_episodes*
DELETE FROM series_episodes WHERE video_id = ANY($1::uuid[])
