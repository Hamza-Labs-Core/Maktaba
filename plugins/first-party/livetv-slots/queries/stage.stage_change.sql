-- name: stage.stage_change
-- mode: read
-- scope: catalog
-- tables: 
SELECT pg_advisory_xact_lock(hashtext('tv_change_draft'))
