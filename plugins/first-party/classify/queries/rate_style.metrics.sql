-- name: rate_style.metrics
-- mode: read
-- scope: subject $1
-- tables: video_signals
SELECT style_metrics, style_version FROM video_signals WHERE video_id = $1
