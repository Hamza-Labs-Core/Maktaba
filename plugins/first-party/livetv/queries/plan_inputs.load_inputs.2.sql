-- name: plan_inputs.load_inputs.2
-- mode: read
-- scope: catalog
-- tables: season_style
SELECT season_id, style, tier, confidence, override_style FROM season_style
