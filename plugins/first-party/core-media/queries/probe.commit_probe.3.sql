-- name: probe.commit_probe.3
-- mode: write
-- scope: subject $1
-- tables: audio_tracks*
UPDATE audio_tracks SET is_default = false
 WHERE video_id = $1 AND track_index >= $2 AND is_default
