-- name: link.write_link
-- mode: write
-- scope: subject $1
-- tables: media_metadata_enrichment*
DELETE FROM media_metadata_enrichment WHERE video_id = $1 AND provider = 'youtube' AND external_id <> $2
