-- name: stage.auto_restage
-- mode: read
-- scope: catalog
-- tables: job_stages
SELECT write_mode FROM job_stages WHERE stage = 'write_channels'
