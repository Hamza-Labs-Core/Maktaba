-- name: duplicates.load_facts.7
-- mode: read
-- scope: catalog
-- tables: playback_state, user_favorites, user_playlist_items
SELECT video_id, COUNT(*) AS n FROM (SELECT video_id FROM playback_state WHERE video_id = ANY($1::uuid[]) UNION ALL SELECT video_id FROM user_playlist_items WHERE video_id IN (SELECT value FROM json_each($1)) UNION ALL SELECT video_id FROM user_favorites WHERE video_id = ANY($1::uuid[])) u GROUP BY video_id
