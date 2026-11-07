-- name: duplicates.load_facts.4
-- mode: read
-- scope: catalog
-- tables: audio_tracks
SELECT video_id, codec FROM audio_tracks WHERE video_id IN (SELECT value FROM json_each($1))
