-- name: edits.carry_over.5
-- mode: write
-- scope: catalog
-- tables: transcript_edits*
INSERT INTO transcript_edits (id, transcript_id, segment_seq, char_start, char_len, before, after, user_id, carried_from) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
