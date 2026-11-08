-- name: tag.run_season
-- mode: read
-- scope: catalog
-- tables: season_tags
SELECT count(*) FROM season_tags WHERE season_id = $1
