-- name: tagging.write_season_tags
-- mode: write
-- scope: catalog
-- tables: season_tags*
DELETE FROM season_tags WHERE season_id = $1
