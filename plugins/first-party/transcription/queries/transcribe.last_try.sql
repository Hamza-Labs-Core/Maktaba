-- name: transcribe.last_try
-- mode: read
-- scope: job $1
-- tables: processing_jobs
SELECT state, error_cause, error FROM processing_jobs WHERE id = $1
