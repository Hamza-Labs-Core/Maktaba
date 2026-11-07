-- name: transcribe.cached_wav
-- mode: read
-- scope: catalog
-- tables: audio_cache, videos
SELECT a.path, a.audio_track_id FROM audio_cache a JOIN videos v ON v.content_hash = a.content_hash WHERE v.id = $1
