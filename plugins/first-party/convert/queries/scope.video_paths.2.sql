-- name: scope.video_paths.2
-- mode: read
-- scope: subject $1
-- tables: file_actions
SELECT old_path, new_path FROM file_actions WHERE video_id = $1
