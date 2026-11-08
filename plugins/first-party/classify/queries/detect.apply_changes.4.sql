-- name: detect.apply_changes.4
-- mode: write
-- scope: series $1, subject $2
-- tables: series_episodes*
INSERT INTO series_episodes (series_id, video_id, season, episode, created_at) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (video_id) DO UPDATE SET series_id = EXCLUDED.series_id, season = EXCLUDED.season, episode = EXCLUDED.episode
