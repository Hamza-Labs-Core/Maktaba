-- name: convert_mp4.run_video.4
-- mode: write
-- scope: subject $1
-- tables: audio_tracks*
UPDATE audio_tracks SET codec = 'aac' WHERE video_id = $1
