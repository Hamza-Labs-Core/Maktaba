-- name: tag.season_transcript_ids.2
-- mode: read
-- scope: catalog
-- tables: transcripts
SELECT id, video_id FROM transcripts WHERE is_active AND video_id IN (SELECT value FROM json_each($1)) ORDER BY video_id, audio_track_id, id
