-- name: detect.input_fingerprint.2
-- mode: read
-- scope: library $1
-- tables: processing_jobs
SELECT j.subject_id AS vid, j.stage AS stage, j.output_hash AS h FROM processing_jobs j WHERE j.library_id = $1 AND j.subject_kind = 'video' AND j.stage IN ('probe','thumb_hash','link','fingerprint') AND j.state = 'done' AND j.outcome IN ('changed','unchanged','skipped_unchanged','imported') AND j.id = (SELECT max(k.id) FROM processing_jobs k WHERE k.subject_kind = 'video'   AND k.subject_id = j.subject_id AND k.stage = j.stage AND k.state = 'done'   AND k.outcome IN ('changed','unchanged','skipped_unchanged','imported')) ORDER BY j.subject_id, j.stage
