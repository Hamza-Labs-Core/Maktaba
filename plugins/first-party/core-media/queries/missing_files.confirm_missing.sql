-- name: missing_files.confirm_missing
-- mode: read
-- scope: library $1
-- tables: processing_jobs, videos
SELECT DISTINCT v.id AS id, v.path AS path
  FROM processing_jobs j
  JOIN videos v ON v.id = j.subject_id
 WHERE j.subject_kind = 'video' AND v.library_id = $1 AND j.state = 'failed'
   AND j.error_cause LIKE 'file_missing%'
   AND NOT EXISTS (SELECT 1 FROM processing_jobs n
                    WHERE n.subject_kind = j.subject_kind AND n.subject_id = j.subject_id
                      AND n.stage = j.stage AND n.id > j.id)
