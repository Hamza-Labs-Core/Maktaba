-- name: second_listen.store.2
-- mode: write
-- scope: subject $1
-- tables: transcripts*
UPDATE transcripts SET is_active = false, superseded_at = $3 WHERE video_id = $1 AND is_active AND id <> $2
