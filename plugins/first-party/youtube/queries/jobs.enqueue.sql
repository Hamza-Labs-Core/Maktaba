-- name: jobs.enqueue
-- mode: read
-- scope: subject $1
-- tables: processing_jobs, videos
SELECT pj.id          AS id,
       pj.finished_at AS finished_at,
       v.updated_at   AS updated_at
  FROM processing_jobs pj
  JOIN videos v ON v.id = pj.video_id
 WHERE pj.video_id = $1
   AND pj.stage    = $2
   AND pj.state    = 'done'
   AND COALESCE(pj.outcome, 'changed') NOT IN ('waiting_inputs', 'awaiting_review')
 ORDER BY pj.finished_at DESC
 LIMIT 1
