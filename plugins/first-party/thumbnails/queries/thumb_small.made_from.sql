-- name: thumb_small.made_from
-- mode: read
-- scope: catalog
-- tables: processing_jobs
SELECT output_summary FROM processing_jobs WHERE subject_kind = 'video' AND subject_id = $1 AND stage = 'thumb_small' AND state = 'done' AND output_summary IS NOT NULL ORDER BY id DESC LIMIT 5
