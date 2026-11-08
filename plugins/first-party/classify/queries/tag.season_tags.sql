-- name: tag.season_tags
-- mode: read
-- scope: subject $1
-- tables: media_metadata_enrichment, videos, youtube_videos
SELECT v.id AS id, v.path AS path, COALESCE(yv.title, v.title, v.filename) AS title, yv.description AS description, COALESCE(v.duration_sec, yv.duration_s) AS dur, yv.upload_date AS upload_date, yv.date_quality AS dq, m.external_id AS yid FROM videos v LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube' LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id WHERE v.id = ANY($1::uuid[])
