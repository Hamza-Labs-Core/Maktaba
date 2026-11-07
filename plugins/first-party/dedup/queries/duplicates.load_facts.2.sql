-- name: duplicates.load_facts.2
-- mode: read
-- scope: catalog
-- tables: media_info, videos
SELECT v.id AS id, v.library_id AS library_id, v.path AS path, v.created_at AS created_at, mi.container AS container, mi.video_codec AS video_codec, mi.width AS width, mi.height AS height, mi.bitrate_kbps AS bitrate_kbps, mi.raw_ffprobe AS raw_ffprobe FROM videos v LEFT JOIN media_info mi ON mi.video_id = v.id WHERE v.id IN (SELECT value FROM json_each($1))
