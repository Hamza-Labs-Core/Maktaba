-- name: jobs_state.read_flags
-- mode: read
-- scope: job $1
-- tables: processing_jobs
SELECT pause_requested, cancel_requested
  FROM processing_jobs
 WHERE id = $1
