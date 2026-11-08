-- name: audit.item_links.2
-- mode: read
-- scope: catalog
-- tables: external_links
SELECT subject_id, external_id FROM external_links WHERE target = 'plex_item' AND subject_kind = 'video' AND external_id IN (SELECT value FROM json_each($1))
