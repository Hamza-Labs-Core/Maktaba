-- name: snapshot.live_streaming
-- mode: read
-- scope: catalog
-- tables: tv_slots
SELECT version FROM tv_slots WHERE role = 'live' AND COALESCE(version, '') <> '' ORDER BY CASE WHEN state = 'healthy' THEN 0 ELSE 1 END, beat_at DESC
