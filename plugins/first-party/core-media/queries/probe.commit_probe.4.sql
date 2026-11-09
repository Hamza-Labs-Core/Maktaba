-- name: probe.commit_probe.4
-- mode: read
-- scope: job_subject $1, subject $1
-- tables: processing_jobs, transcripts
SELECT 1 WHERE EXISTS (SELECT 1 FROM transcripts WHERE video_id = $1 AND is_active) OR EXISTS (SELECT 1 FROM processing_jobs WHERE subject_kind = 'video' AND subject_id = $1 AND stage = 'transcribe' AND state IN ('pending', 'claimed', 'running', 'resuming', 'paused'))
