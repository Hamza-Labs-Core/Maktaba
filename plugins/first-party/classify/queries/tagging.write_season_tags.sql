-- name: tagging.write_season_tags
-- mode: write
-- scope: season $1
-- tables: season_tags*
DELETE FROM season_tags WHERE season_id = $1
