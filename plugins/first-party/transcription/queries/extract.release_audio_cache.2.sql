-- name: extract.release_audio_cache.2
-- mode: write
-- scope: catalog
-- tables: audio_cache*
DELETE FROM audio_cache WHERE content_hash = $1
