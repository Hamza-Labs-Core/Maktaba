-- name: livetv.guide_version
-- mode: read
-- scope: catalog
-- tables: channel_schedule_state
SELECT max(last_generated_at) FROM channel_schedule_state
