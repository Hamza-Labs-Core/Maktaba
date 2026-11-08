-- name: extract.release_audio_cache.2
-- mode: write
-- scope: audio_cache $1
-- tables: audio_cache*
DELETE FROM audio_cache WHERE content_hash = $1
