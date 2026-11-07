-- name: edits.carry_over.4
-- mode: write
-- scope: catalog
-- tables: transcript_segments*
UPDATE transcript_segments SET text = $3, word_data = $4 WHERE transcript_id = $1 AND seq = $2
