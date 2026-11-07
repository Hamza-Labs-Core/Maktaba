-- name: plan_inputs.load_inputs
-- mode: read
-- scope: catalog
-- tables: season_tags
SELECT season_id, tag, score FROM season_tags
