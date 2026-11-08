-- name: rate_style.override
-- mode: read
-- scope: season $1
-- tables: season_style
SELECT override_style FROM season_style WHERE season_id = $1
