-- name: signals.upsert_style
-- mode: write
-- scope: subject $1
-- tables: video_signals*
INSERT INTO video_signals (video_id, style_metrics, style_version, updated_at) VALUES ($1, $2, $3, $4) ON CONFLICT (video_id) DO UPDATE SET style_metrics = EXCLUDED.style_metrics, style_version = EXCLUDED.style_version, updated_at = EXCLUDED.updated_at
