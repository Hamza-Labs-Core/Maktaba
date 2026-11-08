-- name: review_pages.input_fingerprint
-- mode: read
-- scope: library $1
-- tables: processing_jobs
SELECT count(*) AS n, max(id) AS last FROM processing_jobs WHERE stage = 'thumb_small' AND state = 'done' AND library_id = $1
