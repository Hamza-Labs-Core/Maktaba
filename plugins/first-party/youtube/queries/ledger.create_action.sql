-- name: ledger.create_action
-- mode: write
-- scope: subject $3
-- tables: file_actions*
INSERT INTO file_actions (id, library_id, video_id, job_id, kind, state, old_path, new_path, trash_path, old_hash, new_hash, old_size, new_size, verify, purge_after, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16)
