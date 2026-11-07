-- name: edits.carry_over.2
-- mode: read
-- scope: catalog
-- tables: transcripts, videos
SELECT t.video_id, t.audio_track_id, v.library_id FROM transcripts t JOIN videos v ON v.id = t.video_id WHERE t.id = $1
