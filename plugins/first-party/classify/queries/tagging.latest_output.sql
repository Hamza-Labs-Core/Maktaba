-- name: tagging.latest_output
-- mode: read
-- scope: catalog
-- tables: processing_jobs
SELECT output_hash FROM processing_jobs WHERE subject_kind = $1 AND subject_id = $2 AND stage = $3 AND state = 'done' AND outcome IN ('changed','unchanged','skipped_unchanged','imported') ORDER BY id DESC LIMIT 1
