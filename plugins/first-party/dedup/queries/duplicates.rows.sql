-- name: duplicates.rows
-- mode: read
-- scope: library $1
-- tables: video_signals, videos
SELECT v.id AS id, v.library_id AS library_id, v.path AS path, v.content_hash AS content_hash, v.size_bytes AS size_bytes, COALESCE(v.display_title, v.title, v.filename) AS title, v.duration_sec AS duration, vs.frame_hashes AS frames FROM videos v LEFT JOIN video_signals vs ON vs.video_id = v.id WHERE v.library_id = $1 AND v.state NOT IN ('superseded', 'missing') AND v.superseded_by IS NULL
