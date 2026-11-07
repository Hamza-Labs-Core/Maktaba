-- name: transcribe.store.3
-- mode: write
-- scope: catalog
-- tables: videos*
UPDATE videos SET detected_language = $2 WHERE id = $1 AND detected_language IS NULL
