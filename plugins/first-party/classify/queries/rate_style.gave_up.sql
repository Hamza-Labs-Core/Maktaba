-- name: rate_style.gave_up
-- mode: read
-- scope: catalog
-- tables: processing_jobs
SELECT state, attempts, max_attempts FROM processing_jobs WHERE subject_kind = 'video' AND subject_id = $1 AND stage = 'style_sample' ORDER BY id DESC LIMIT 1
