-- name: groups.planned.3
-- mode: read
-- scope: library $1
-- tables: clip_groups
SELECT id, title FROM clip_groups WHERE library_id = $1 ORDER BY kind, title, id
