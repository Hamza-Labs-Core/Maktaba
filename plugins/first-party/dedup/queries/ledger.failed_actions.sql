-- name: ledger.failed_actions
-- mode: read
-- scope: catalog
-- tables: file_actions
SELECT * FROM file_actions WHERE kind = $1 AND state = 'failed' ORDER BY created_at DESC, id DESC LIMIT $2
