-- name: second_listen.store.4
-- mode: write
-- scope: catalog
-- tables: transcript_segments*
UPDATE transcript_segments SET metadata = metadata - 'words' WHERE transcript_id = $1 AND metadata ? 'words'
