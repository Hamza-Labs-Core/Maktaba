-- name: second_listen.active
-- mode: read
-- scope: subject $1
-- tables: transcripts
SELECT id, audio_track_id, language, detected_language, backend, model, backend_version, word_level, diarized, metadata FROM transcripts WHERE video_id = $1 AND is_active
