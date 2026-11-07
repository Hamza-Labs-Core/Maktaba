-- name: stage.write_plan_pending.2
-- mode: read
-- scope: catalog
-- tables: channels
SELECT id, library_id, number, name, slug, logo_path, category, mode, mode_config, source_filter, transition, enabled, sort_order FROM channels WHERE id = $1
