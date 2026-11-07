-- name: write_channels.compute_changes
-- mode: read
-- scope: catalog
-- tables: external_links
SELECT subject_id, external_id, content_hash, created_by_tool FROM external_links WHERE target = $1 AND subject_kind = $2
