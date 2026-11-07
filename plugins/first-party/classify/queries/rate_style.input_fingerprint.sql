-- name: rate_style.input_fingerprint
-- mode: read
-- scope: catalog
-- tables: processing_jobs
SELECT output_hash FROM processing_jobs WHERE subject_kind = 'video' AND subject_id = $1 AND stage = 'style_sample' AND state = 'done' AND outcome IN ('changed','unchanged','skipped_unchanged','imported') ORDER BY id DESC LIMIT 1
