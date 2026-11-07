-- name: write_channels.apply_changes.5
-- mode: read
-- scope: catalog
-- tables: external_links
SELECT external_id FROM external_links WHERE target = $1 AND subject_kind = $2
