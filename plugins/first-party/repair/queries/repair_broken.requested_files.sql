-- name: repair_broken.requested_files
-- mode: read
-- scope: library $1
-- tables: item_requests, media_metadata_enrichment, videos
SELECT r.id AS request_id, v.id AS id, v.path AS path, m.external_id AS youtube_id
  FROM item_requests r
  JOIN videos v ON v.id = r.video_id
  LEFT JOIN media_metadata_enrichment m
    ON m.video_id = v.id AND m.provider = 'youtube' AND NOT m.is_dismissed
 WHERE v.library_id = $1 AND r.action = 'redownload' AND r.done_at IS NULL
 ORDER BY r.id
