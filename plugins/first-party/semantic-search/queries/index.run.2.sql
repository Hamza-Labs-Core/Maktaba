-- name: index.run.2
-- mode: read
-- scope: subject $1
-- tables: transcript_segments, transcripts
SELECT s.transcript_id AS transcript_id, s.seq AS seq FROM transcript_segments s JOIN transcripts t ON t.id = s.transcript_id WHERE t.video_id = $1 AND t.id <> $2
