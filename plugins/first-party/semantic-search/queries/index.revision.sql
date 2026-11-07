-- name: index.revision
-- mode: read
-- scope: catalog
-- tables: transcripts
SELECT COALESCE(revision, 0) FROM transcripts WHERE id = $1
