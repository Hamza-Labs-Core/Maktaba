-- name: transcribe.store
-- mode: write
-- scope: subject $1
-- tables: transcripts*
INSERT INTO transcripts (id, video_id, audio_track_id, language, detected_language,
                                     backend, model, backend_version, word_level, diarized,
                                     is_active, metadata)
            VALUES ($8, $1, $7, $2, $9, $10, $3, $4, $5, false, false, $6)
            RETURNING id
