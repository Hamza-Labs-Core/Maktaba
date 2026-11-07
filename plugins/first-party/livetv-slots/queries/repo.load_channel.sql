-- name: repo.load_channel
-- mode: read
-- scope: catalog
-- tables: channels
SELECT id, library_id, number, mode, mode_config, source_filter, transition, enabled
            FROM channels WHERE id = $1
