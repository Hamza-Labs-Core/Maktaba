-- name: signals.upsert_thumb
-- mode: write
-- scope: subject $1
-- tables: video_signals*
INSERT INTO video_signals (video_id, thumb_dhash, thumb_version, updated_at) VALUES ($1, $2, $3, $4) ON CONFLICT (video_id) DO UPDATE SET thumb_dhash = EXCLUDED.thumb_dhash, thumb_version = EXCLUDED.thumb_version, updated_at = EXCLUDED.updated_at
