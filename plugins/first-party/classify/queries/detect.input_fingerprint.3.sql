-- name: detect.input_fingerprint.3
-- mode: read
-- scope: catalog
-- tables: processing_jobs
SELECT output_hash FROM processing_jobs WHERE subject_kind = 'library' AND subject_id = $1 AND stage = 'youtube_sync' AND state = 'done' AND outcome IN ('changed','unchanged','skipped_unchanged','imported') ORDER BY id DESC LIMIT 1
