-- name: transcribe.store.2
-- mode: write
-- scope: subject $1
-- tables: transcribe_checkpoints*
DELETE FROM transcribe_checkpoints WHERE video_id = $1
