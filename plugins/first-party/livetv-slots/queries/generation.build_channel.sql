-- name: generation.build_channel
-- mode: read
-- scope: catalog
-- tables: 
SELECT pg_advisory_xact_lock($1, $2)
