-- name: link.run
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, videos
SELECT m.external_id AS e FROM media_metadata_enrichment m JOIN videos x ON x.id = m.video_id WHERE x.library_id = $1 AND m.provider = 'youtube' AND m.video_id <> $2
