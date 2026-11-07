-- name: livetv.save
-- mode: write
-- scope: catalog
-- tables: plex_livetv_state*
INSERT INTO plex_livetv_state (id, status, checked_at, guide_reloaded_at, guide_version, channelmap_at, channelmap_hash, last_error, last_error_at, updated_at) VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8, $2) ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, checked_at = EXCLUDED.checked_at, guide_reloaded_at = EXCLUDED.guide_reloaded_at, guide_version = EXCLUDED.guide_version, channelmap_at = EXCLUDED.channelmap_at, channelmap_hash = EXCLUDED.channelmap_hash, last_error = EXCLUDED.last_error, last_error_at = COALESCE(EXCLUDED.last_error_at, plex_livetv_state.last_error_at), updated_at = EXCLUDED.updated_at
