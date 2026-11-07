-- name: repo.all_slot_lanes
-- mode: read
-- scope: catalog
-- tables: channels
SELECT mode_config FROM channels WHERE enabled = true
