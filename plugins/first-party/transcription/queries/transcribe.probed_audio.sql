-- name: transcribe.probed_audio
-- mode: read
-- scope: subject $1
-- tables: media_info
SELECT raw_ffprobe FROM media_info WHERE video_id = $1
