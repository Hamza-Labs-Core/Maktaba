-- name: probe.run.2
-- mode: read
-- scope: job_subject $1
-- tables: processing_jobs
SELECT count(*) FROM processing_jobs WHERE subject_kind = 'video' AND subject_id = $1 AND stage = 'content_hash'
