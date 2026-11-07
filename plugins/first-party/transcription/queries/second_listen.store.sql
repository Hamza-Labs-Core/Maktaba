-- name: second_listen.store
-- mode: write
-- scope: subject $2
-- tables: transcripts*
INSERT INTO transcripts (id, video_id, audio_track_id, language, detected_language,
                                     backend, model, backend_version, word_level, diarized,
                                     is_active, metadata)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, false, $11)
