-- name: tagging.write_season_tags.2
-- mode: write
-- scope: catalog
-- tables: season_tags*
INSERT INTO season_tags (season_id, tag, score, evidence) VALUES ($1,$2,$3,$4)
