-- name: detect.delete_episodes.2
-- mode: write
-- scope: subject $1
-- tables: series_episodes*
DELETE FROM series_episodes WHERE video_id IN (SELECT value FROM json_each($1))
