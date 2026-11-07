-- name: edits.carry_over.7
-- mode: write
-- scope: subject $3
-- tables: word_flags*
INSERT INTO word_flags (id, kind, transcript_id, video_id, library_id, audio_track_id, segment_seq, char_start, char_len, start_sec, end_sec, original, context, reasons, scores, priority, candidates, key, status) VALUES ($1, 'word', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, 'open') ON CONFLICT (transcript_id, segment_seq, char_start) DO NOTHING
