-- name: commit.lock
-- mode: read
-- scope: catalog
-- tables: 
SELECT pg_advisory_xact_lock(hashtext($1))
