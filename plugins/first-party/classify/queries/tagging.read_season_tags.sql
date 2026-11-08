-- name: tagging.read_season_tags
-- mode: read
-- scope: catalog
-- tables: season_tags
SELECT tag, score, evidence FROM season_tags WHERE season_id = $1
