-- name: second_listen.store.5
-- mode: write
-- scope: catalog
-- tables: transcript_segments*
UPDATE transcript_segments SET metadata = json_remove(metadata, '$.words') WHERE transcript_id = $1 AND json_extract(metadata, '$.words') IS NOT NULL
