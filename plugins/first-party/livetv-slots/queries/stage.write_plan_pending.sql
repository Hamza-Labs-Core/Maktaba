-- name: stage.write_plan_pending
-- mode: read
-- scope: catalog
-- tables: external_links
SELECT external_id FROM external_links WHERE target = $1 AND subject_kind = $2
