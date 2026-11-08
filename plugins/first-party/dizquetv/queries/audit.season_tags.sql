-- name: audit.season_tags
-- mode: read
-- scope: catalog
-- tables: season_tags
SELECT season_id, tag, score, evidence FROM season_tags WHERE season_id = ANY($1::uuid[])
