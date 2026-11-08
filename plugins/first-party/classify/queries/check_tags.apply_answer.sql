-- name: check_tags.apply_answer
-- mode: write
-- scope: catalog
-- tables: tag_checks*
UPDATE tag_checks SET applied_hash = $2 WHERE season_id = $1
