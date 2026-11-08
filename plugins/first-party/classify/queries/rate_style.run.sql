-- name: rate_style.run
-- mode: read
-- scope: season $1
-- tables: season_style
SELECT style, tier, confidence FROM season_style WHERE season_id = $1
