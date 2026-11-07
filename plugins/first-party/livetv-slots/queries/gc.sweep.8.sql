-- name: gc.sweep.8
-- mode: read
-- scope: catalog
-- tables: tv_changes
SELECT diff FROM tv_changes WHERE state = 'draft'
