-- name: extract.release_audio_cache
-- mode: read
-- scope: audio_cache $1
-- tables: audio_cache
SELECT path FROM audio_cache WHERE content_hash = $1
