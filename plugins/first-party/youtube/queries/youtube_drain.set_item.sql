-- name: youtube_drain.set_item
-- mode: write
-- scope: library $1
-- tables: youtube_downloads*
UPDATE youtube_downloads SET state = $3, attempts = attempts + $4, bytes = CASE WHEN $5 THEN $6 ELSE bytes END, done_at = CASE WHEN $7 THEN $8 ELSE done_at END, error = CASE WHEN $9 THEN $10 ELSE error END, failure_class = CASE WHEN $11 THEN $12 ELSE failure_class END, not_before = CASE WHEN $13 THEN $14 ELSE not_before END, reason = CASE WHEN $15 THEN $16 ELSE reason END, video_id = CASE WHEN $17 THEN $18 ELSE video_id END WHERE library_id = $1 AND youtube_id = $2
