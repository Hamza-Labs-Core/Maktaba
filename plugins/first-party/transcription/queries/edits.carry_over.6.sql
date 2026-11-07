-- name: edits.carry_over.6
-- mode: write
-- scope: catalog
-- tables: transcripts*
UPDATE transcripts SET revision = revision + 1 WHERE id = $1
