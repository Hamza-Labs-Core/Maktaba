-- name: plex_collection.input_fingerprint
-- mode: read
-- scope: catalog
-- tables: job_stages
SELECT write_mode FROM job_stages WHERE stage = $1
