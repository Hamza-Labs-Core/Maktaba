-- name: fp_gate.fingerprinted
-- mode: read
-- scope: subject $1
-- tables: video_signals
SELECT fp_version, fp_partial, audio_head, audio_tail, frame_hashes FROM video_signals WHERE video_id = $1
