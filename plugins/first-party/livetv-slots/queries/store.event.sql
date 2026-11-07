-- name: store.event
-- mode: write
-- scope: catalog
-- tables: tv_change_events*
INSERT INTO tv_change_events (change_id, at, actor_user_id, actor_source, from_state, to_state, action, detail) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
