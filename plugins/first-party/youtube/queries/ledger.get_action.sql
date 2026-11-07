-- name: ledger.get_action
-- mode: read
-- scope: catalog
-- tables: file_actions
SELECT * FROM file_actions WHERE id = $1
