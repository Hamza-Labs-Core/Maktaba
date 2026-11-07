-- name: plan_channels.insert
-- mode: write
-- scope: catalog
-- tables: channel_plans*
INSERT INTO channel_plans (created_at, planner_version, input_hash, plan) VALUES ($1,$2,$3,$4)
