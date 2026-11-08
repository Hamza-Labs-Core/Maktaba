-- name: transcribe.input_fingerprint
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT content_hash FROM videos WHERE id = $1
