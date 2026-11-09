-- name: tag.run_season.2
-- mode: read
-- scope: season $1
-- tables: season_tags
SELECT tag, score FROM season_tags WHERE season_id = $1
