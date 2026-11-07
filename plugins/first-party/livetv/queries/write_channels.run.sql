-- name: write_channels.run
-- mode: read
-- scope: catalog
-- tables: external_links
SELECT subject_id, content_hash FROM external_links WHERE target = $1 AND subject_kind = $2 ORDER BY subject_id
