-- name: second_listen.skipped
-- mode: write
-- scope: transcript $1
-- tables: transcripts*
UPDATE transcripts SET metadata = $2 WHERE id = $1
