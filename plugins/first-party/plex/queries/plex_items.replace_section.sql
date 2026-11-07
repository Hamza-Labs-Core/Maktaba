-- name: plex_items.replace_section
-- mode: write
-- scope: catalog
-- tables: plex_items*
DELETE FROM plex_items WHERE server = $1 AND section_key = $2
