-- name: transcribe.input_fingerprint
-- mode: read
-- scope: catalog
-- tables: videos
SELECT content_hash FROM videos WHERE id = $1
