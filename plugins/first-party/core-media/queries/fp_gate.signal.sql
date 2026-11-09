-- name: fp_gate.signal
-- mode: read
-- scope: subject $1
-- tables: video_signals
SELECT audio_head, audio_tail, frame_hashes FROM video_signals WHERE video_id = $1
