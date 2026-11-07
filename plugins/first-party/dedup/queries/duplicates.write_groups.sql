-- name: duplicates.write_groups
-- mode: read
-- scope: catalog
-- tables: 
SELECT pg_advisory_xact_lock(hashtext('maktaba.duplicate_groups'))
