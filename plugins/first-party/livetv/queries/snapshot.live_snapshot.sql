-- name: snapshot.live_snapshot
-- mode: read
-- scope: catalog
-- tables: channel_art_overrides
SELECT channel_id, kind, path, settings, base FROM channel_art_overrides
