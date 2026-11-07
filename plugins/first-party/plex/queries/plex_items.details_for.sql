-- name: plex_items.details_for
-- mode: read
-- scope: catalog
-- tables: plex_items
SELECT rating_key, detail FROM plex_items WHERE rating_key = ANY($1::text[])
