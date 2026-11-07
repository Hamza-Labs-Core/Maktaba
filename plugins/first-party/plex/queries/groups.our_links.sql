-- name: groups.our_links
-- mode: read
-- scope: catalog
-- tables: external_links
SELECT target, subject_kind, subject_id, external_id, title, content_hash, created_by_tool FROM external_links WHERE target IN ($1, $2) AND content_hash LIKE $3
