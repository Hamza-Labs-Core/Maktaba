-- name: livetv.save.2
-- mode: write
-- scope: catalog
-- tables: plex_livetv_state*
UPDATE plex_livetv_state SET register_requested_at = NULL WHERE id = 1 AND register_requested_at <= $1
