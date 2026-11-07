-- name: second_listen.store.3
-- mode: write
-- scope: catalog
-- tables: transcripts*
UPDATE transcripts SET is_active = true WHERE id = $1
