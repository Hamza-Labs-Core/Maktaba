-- name: scope.restore_paths
-- mode: read
-- scope: file_action $1, library $2
-- tables: file_actions
SELECT old_path, new_path FROM file_actions WHERE id = $1 AND library_id = $2
