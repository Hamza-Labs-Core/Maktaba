-- name: trash.due_actions
-- mode: read
-- scope: library $1
-- tables: file_actions
SELECT * FROM file_actions WHERE library_id = $1 AND trash_path IS NOT NULL AND purged_at IS NULL AND state = 'done' AND purge_after IS NOT NULL AND purge_after <= $2 ORDER BY purge_after, id
