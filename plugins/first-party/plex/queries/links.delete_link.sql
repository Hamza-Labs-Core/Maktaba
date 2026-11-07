-- name: links.delete_link
-- mode: write
-- scope: catalog
-- tables: external_links*
DELETE FROM external_links WHERE target = $1 AND subject_kind = $2 AND subject_id = $3
