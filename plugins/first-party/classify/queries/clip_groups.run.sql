-- name: clip_groups.run
-- mode: write
-- scope: library $1
-- tables: clip_groups*
DELETE FROM clip_groups WHERE library_id = $1
