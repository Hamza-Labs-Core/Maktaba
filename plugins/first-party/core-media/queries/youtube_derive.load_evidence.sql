-- name: youtube_derive.load_evidence
-- mode: read
-- scope: catalog
-- tables: libraries, media_metadata_enrichment, videos, youtube_videos
SELECT v.library_id AS lib, m.external_id AS yid, yv.channel_id AS cid FROM media_metadata_enrichment m JOIN videos v ON v.id = m.video_id JOIN libraries l ON l.id = v.library_id LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id WHERE m.provider = 'youtube' AND v.deleted_at IS NULL AND l.deleted_at IS NULL
