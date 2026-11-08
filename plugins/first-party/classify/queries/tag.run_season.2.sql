-- name: tag.run_season.2
-- mode: read
-- scope: catalog
-- tables: season_tags
SELECT tag, score FROM season_tags WHERE season_id = $1
