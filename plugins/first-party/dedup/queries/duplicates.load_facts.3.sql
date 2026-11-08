-- name: duplicates.load_facts.3
-- mode: read
-- scope: catalog
-- tables: audio_tracks
SELECT video_id, codec FROM audio_tracks WHERE video_id = ANY($1::uuid[])
