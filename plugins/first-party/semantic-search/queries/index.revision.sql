-- name: index.revision
-- mode: read
-- scope: transcript $1
-- tables: transcripts
SELECT COALESCE(revision, 0) FROM transcripts WHERE id = $1
