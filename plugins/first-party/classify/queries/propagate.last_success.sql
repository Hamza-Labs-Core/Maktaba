-- name: propagate.last_success
-- mode: read
-- scope: catalog
-- tables: processing_jobs
SELECT id, stage_version, input_hash, output_hash FROM processing_jobs WHERE subject_kind = $1 AND subject_id = $2 AND stage = $3 AND state = 'done' AND id <> $4 AND outcome IN ('changed','unchanged','skipped_unchanged','imported') ORDER BY id DESC LIMIT 1
