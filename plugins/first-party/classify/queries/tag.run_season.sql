-- name: tag.run_season
-- mode: read
-- scope: season $1
-- tables: season_tags
SELECT count(*) FROM season_tags WHERE season_id = $1
