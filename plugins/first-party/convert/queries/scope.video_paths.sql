-- name: scope.video_paths
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT path FROM videos WHERE id = $1
