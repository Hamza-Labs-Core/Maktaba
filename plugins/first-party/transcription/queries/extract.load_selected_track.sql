-- name: extract.load_selected_track
-- mode: read
-- scope: subject $1
-- tables: audio_tracks
SELECT id, track_index, codec, channels, sample_rate, language, title,
       is_default, disposition
  FROM audio_tracks
 WHERE video_id = $1
 ORDER BY track_index
