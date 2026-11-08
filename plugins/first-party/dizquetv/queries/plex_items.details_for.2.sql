-- name: plex_items.details_for.2
-- mode: read
-- scope: catalog
-- tables: plex_items
SELECT rating_key, detail FROM plex_items WHERE rating_key IN (SELECT value FROM json_each($1))
