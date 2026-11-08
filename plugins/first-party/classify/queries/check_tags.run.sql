-- name: check_tags.run
-- mode: read
-- scope: catalog
-- tables: tag_checks
SELECT input_hash, answer, applied_hash FROM tag_checks WHERE season_id = $1
