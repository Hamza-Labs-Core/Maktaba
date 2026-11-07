-- name: transcribe.save_checkpoint
-- mode: write
-- scope: subject $1
-- tables: transcribe_checkpoints*
INSERT INTO transcribe_checkpoints (video_id, fingerprint, seek_frames, last_speech, language, segments, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (video_id) DO UPDATE SET fingerprint = EXCLUDED.fingerprint, seek_frames = EXCLUDED.seek_frames, last_speech = EXCLUDED.last_speech, language = EXCLUDED.language, segments = CASE WHEN transcribe_checkpoints.fingerprint = EXCLUDED.fingerprint THEN transcribe_checkpoints.segments || EXCLUDED.segments ELSE EXCLUDED.segments END, updated_at = EXCLUDED.updated_at
