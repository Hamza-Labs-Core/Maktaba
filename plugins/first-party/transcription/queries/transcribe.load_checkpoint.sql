-- name: transcribe.load_checkpoint
-- mode: read
-- scope: subject $1
-- tables: transcribe_checkpoints
SELECT fingerprint, seek_frames, last_speech, language, segments FROM transcribe_checkpoints WHERE video_id = $1
