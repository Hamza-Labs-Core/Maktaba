-- name: dedup_merge.apply_group.2
-- mode: read
-- scope: catalog
-- tables: duplicate_members, videos
SELECT m.video_id AS video_id, m.path AS path, m.is_copy AS is_copy, m.score AS score, v.path AS own, v.state AS state, v.library_id AS library_id, v.content_hash AS content_hash, v.size_bytes AS size_bytes FROM duplicate_members m JOIN videos v ON v.id = m.video_id WHERE m.group_id = $1 ORDER BY m.video_id, m.is_copy, m.path
