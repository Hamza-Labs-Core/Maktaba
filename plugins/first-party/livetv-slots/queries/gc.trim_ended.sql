-- name: gc.trim_ended
-- mode: write
-- scope: catalog
-- tables: tv_changes*
UPDATE tv_changes SET diff = diff #- '{pending,pools}' WHERE state IN ('failed','cancelled','superseded') AND (state <> 'superseded' OR swapped_at IS NULL) AND diff -> 'pending' ? 'pools' RETURNING id
