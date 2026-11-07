-- name: second_listen.store.6
-- mode: write
-- scope: catalog
-- tables: transcript_segments*
UPDATE transcript_segments SET word_data = NULL WHERE transcript_id = $1 AND word_data IS NOT NULL
