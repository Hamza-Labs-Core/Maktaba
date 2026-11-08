-- name: tagging.read_season_tags
-- mode: read
-- scope: season $1
-- tables: season_tags
SELECT tag, score, evidence FROM season_tags WHERE season_id = $1
