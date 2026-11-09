-- name: signals.upsert_fingerprint
-- mode: write
-- scope: subject $1
-- tables: video_signals*
INSERT INTO video_signals (video_id, audio_head, audio_tail, frame_hashes, fp_version, fp_partial, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (video_id) DO UPDATE SET audio_head = EXCLUDED.audio_head, audio_tail = EXCLUDED.audio_tail, frame_hashes = EXCLUDED.frame_hashes, fp_version = EXCLUDED.fp_version, fp_partial = EXCLUDED.fp_partial, updated_at = EXCLUDED.updated_at
