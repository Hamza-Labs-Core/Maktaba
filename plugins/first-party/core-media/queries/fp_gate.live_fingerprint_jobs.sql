-- name: fp_gate.live_fingerprint_jobs
-- mode: read
-- scope: job_subject $1
-- tables: processing_jobs
SELECT 1 AS x FROM processing_jobs WHERE subject_kind = 'video' AND subject_id = $1 AND stage = 'fingerprint' AND state IN ('pending','claimed','running','resuming','paused') LIMIT 1
