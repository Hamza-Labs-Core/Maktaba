-- name: merge.live_defs
-- mode: read
-- scope: catalog
-- tables: channels
SELECT id, library_id, number, name, slug, logo_path, category, mode, mode_config, source_filter, transition, enabled, sort_order FROM channels WHERE lifecycle = 'live'
