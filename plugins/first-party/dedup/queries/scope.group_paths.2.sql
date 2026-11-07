-- name: scope.group_paths.2
-- mode: read
-- scope: catalog
-- tables: duplicate_members, videos
SELECT v.path FROM videos v JOIN duplicate_members m ON m.video_id = v.id WHERE m.group_id = $1
