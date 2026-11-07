-- name: youtube_drain.yield_reason.2
-- mode: read
-- scope: catalog
-- tables: job_stages, processing_jobs
SELECT j.stage AS stage FROM processing_jobs j JOIN job_stages s ON s.stage = j.stage WHERE s.pool = (SELECT pool FROM job_stages WHERE stage = $1) AND j.id <> $2 AND j.state = 'pending' AND j.priority < $3 AND s.enabled AND NOT s.paused AND j.parked_on IS NULL AND NOT j.cancel_requested AND NOT j.pause_requested AND (j.not_before IS NULL OR j.not_before <= $4) ORDER BY j.priority, j.id LIMIT 1
