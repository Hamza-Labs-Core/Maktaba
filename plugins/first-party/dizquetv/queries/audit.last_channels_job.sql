-- name: audit.last_channels_job
-- mode: read
-- scope: catalog
-- tables: processing_jobs
SELECT max(id) AS id FROM processing_jobs WHERE stage = 'dizquetv_channels' AND state = 'done'
