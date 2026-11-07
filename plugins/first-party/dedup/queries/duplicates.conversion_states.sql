-- name: duplicates.conversion_states
-- mode: read
-- scope: library $1
-- tables: processing_jobs, videos
SELECT j.subject_id AS vid, j.state AS state, j.payload AS payload FROM processing_jobs j JOIN videos v ON v.id = j.subject_id WHERE j.subject_kind = 'video' AND j.stage = 'convert_mp4' AND v.library_id = $1 ORDER BY j.id
