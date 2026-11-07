-- name: transcribe.insert_segments
-- mode: write
-- scope: catalog
-- tables: transcript_segments*
INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text, confidence, metadata, word_data) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
