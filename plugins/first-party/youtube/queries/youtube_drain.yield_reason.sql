-- name: youtube_drain.yield_reason
-- mode: read
-- scope: catalog
-- tables: job_stages
SELECT paused FROM job_stages WHERE stage = $1
