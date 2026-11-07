-- name: second_listen.skipped
-- mode: write
-- scope: catalog
-- tables: transcripts*
UPDATE transcripts SET metadata = $2 WHERE id = $1
