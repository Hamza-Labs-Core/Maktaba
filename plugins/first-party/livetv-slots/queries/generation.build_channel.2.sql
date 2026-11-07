-- name: generation.build_channel.2
-- mode: read
-- scope: catalog
-- tables: 
SELECT pg_advisory_xact_lock_shared(hashtext($1))
