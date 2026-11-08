-- name: ledger.get_action
-- mode: read
-- scope: file_action $1
-- tables: file_actions
SELECT * FROM file_actions WHERE id = $1
