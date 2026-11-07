-- name: write_channels.input_fingerprint.2
-- mode: read
-- scope: catalog
-- tables: job_stages
SELECT write_mode FROM job_stages WHERE stage = 'write_channels'
