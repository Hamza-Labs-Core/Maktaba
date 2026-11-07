-- name: store.draft_with_digest
-- mode: read
-- scope: catalog
-- tables: tv_changes
SELECT * FROM tv_changes WHERE state = 'draft' AND digest = $1 ORDER BY created_at DESC LIMIT 1
