-- name: tag.loose_transcript_ids
-- mode: read
-- scope: library $1
-- tables: series_episodes, transcripts, videos
SELECT t.id AS id, t.video_id AS video_id
  FROM transcripts t
  JOIN videos v ON v.id = t.video_id
 WHERE v.library_id = $1 AND v.deleted_at IS NULL AND t.is_active
   AND NOT EXISTS (SELECT 1 FROM series_episodes e WHERE e.video_id = v.id)
 ORDER BY t.video_id, t.audio_track_id, t.id
