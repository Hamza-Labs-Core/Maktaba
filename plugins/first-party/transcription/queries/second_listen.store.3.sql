-- name: second_listen.store.3
-- mode: write
-- scope: transcript $1
-- tables: transcripts*
UPDATE transcripts SET is_active = true WHERE id = $1
