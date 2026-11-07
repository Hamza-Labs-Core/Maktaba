-- name: write_channels.input_fingerprint.3
-- mode: read
-- scope: catalog
-- tables: external_links
SELECT subject_id FROM external_links WHERE target = $1 AND subject_kind = $2 AND content_hash = $3 ORDER BY subject_id
