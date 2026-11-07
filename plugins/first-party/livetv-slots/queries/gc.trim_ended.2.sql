-- name: gc.trim_ended.2
-- mode: write
-- scope: catalog
-- tables: tv_changes*
UPDATE tv_changes SET diff = json_remove(diff, '$.pending.pools') WHERE state IN ('failed','cancelled','superseded') AND (state <> 'superseded' OR swapped_at IS NULL) AND json_type(diff, '$.pending.pools') IS NOT NULL RETURNING id
