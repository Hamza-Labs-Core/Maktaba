-- name: ledger.open_actions
-- mode: read
-- scope: catalog
-- tables: file_actions, processing_jobs
SELECT fa.* FROM file_actions fa WHERE fa.state IN ('planned', 'staged', 'verified', 'swapped') AND ($1::uuid IS NULL OR fa.library_id = $1::uuid) AND ($2::uuid IS NULL OR fa.video_id = $2::uuid) AND ($3::boolean OR NOT EXISTS (SELECT 1 FROM processing_jobs pj WHERE pj.id = fa.job_id AND pj.state IN ('claimed', 'running', 'resuming'))) ORDER BY fa.created_at, fa.id
