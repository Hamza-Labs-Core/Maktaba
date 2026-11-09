-- name: link.write_link.2
-- mode: read
-- scope: subject $1
-- tables: media_field_provenance
SELECT 1 AS x FROM media_field_provenance WHERE video_id = $1 AND origin = 'user' LIMIT 1
