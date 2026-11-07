-- name: pass.topup_once
-- mode: read
-- scope: catalog
-- tables: 
SELECT pg_try_advisory_xact_lock($1, $2)
