-- name: links.get_link
-- mode: read
-- scope: catalog
-- tables: external_links
SELECT target, subject_kind, subject_id, external_id, title, content_hash, created_by_tool FROM external_links WHERE target = $1 AND subject_kind = $2 AND subject_id = $3
