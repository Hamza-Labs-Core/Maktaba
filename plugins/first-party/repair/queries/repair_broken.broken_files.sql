-- name: repair_broken.broken_files
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, processing_jobs, videos
SELECT v.id AS id, v.path AS path, m.external_id AS youtube_id
  FROM videos v
  LEFT JOIN media_metadata_enrichment m
    ON m.video_id = v.id AND m.provider = 'youtube' AND NOT m.is_dismissed
 WHERE v.library_id = $1 AND v.deleted_at IS NULL
   AND (SELECT j.state FROM processing_jobs j
         WHERE j.video_id = v.id AND j.stage = 'probe' ORDER BY j.id DESC LIMIT 1) = 'failed'
 ORDER BY v.path
