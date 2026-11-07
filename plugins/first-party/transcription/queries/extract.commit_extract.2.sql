-- name: extract.commit_extract.2
-- mode: write
-- scope: catalog
-- tables: audio_tracks*
UPDATE audio_tracks SET last_extracted_at = $2 WHERE id = $1
