-- name: access_check.run
-- mode: write
-- scope: subject $1
-- tables: item_checks*
INSERT INTO item_checks (video_id, ok, error, host, checked_at) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (video_id) DO UPDATE SET ok = EXCLUDED.ok, error = EXCLUDED.error, host = EXCLUDED.host, checked_at = EXCLUDED.checked_at
