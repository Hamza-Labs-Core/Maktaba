-- name: transcribe.chosen_track
-- mode: read
-- scope: subject $1
-- tables: audio_tracks
SELECT id FROM audio_tracks WHERE video_id = $1 AND track_index = $2
