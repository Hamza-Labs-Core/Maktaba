-- name: audit.season_tags.2
-- mode: read
-- scope: catalog
-- tables: season_tags
SELECT season_id, tag, score, evidence FROM season_tags WHERE season_id IN (SELECT value FROM json_each($1))
