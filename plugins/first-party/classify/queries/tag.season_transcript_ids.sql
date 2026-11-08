-- name: tag.season_transcript_ids
-- mode: read
-- scope: catalog
-- tables: transcripts
SELECT id, video_id FROM transcripts WHERE is_active AND video_id = ANY($1::uuid[]) ORDER BY video_id, audio_track_id, id
