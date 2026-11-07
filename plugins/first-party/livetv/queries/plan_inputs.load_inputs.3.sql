-- name: plan_inputs.load_inputs.3
-- mode: read
-- scope: catalog
-- tables: episode_tags
SELECT video_id, tag, score FROM episode_tags
