-- name: youtube_sources.seed_done
-- mode: write
-- scope: subject $5
-- tables: youtube_downloads*
INSERT INTO youtube_downloads (library_id, youtube_id, state, backfill, reason, video_id, requested_at, done_at) VALUES ($1,$2,'done',$3,$4,$5,$6,$6) ON CONFLICT (library_id, youtube_id) DO UPDATE SET state = 'done', reason = EXCLUDED.reason, done_at = EXCLUDED.done_at, video_id = COALESCE(youtube_downloads.video_id, EXCLUDED.video_id), failure_class = NULL, error = NULL, not_before = NULL WHERE youtube_downloads.state NOT IN ('done', 'downloading') RETURNING youtube_id
