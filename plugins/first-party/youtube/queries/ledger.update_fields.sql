-- name: ledger.update_fields
-- mode: write
-- scope: catalog
-- tables: file_actions*
UPDATE file_actions SET updated_at = $2, error = CASE WHEN $3 THEN $4 ELSE error END, new_hash = CASE WHEN $5 THEN $6 ELSE new_hash END, new_path = CASE WHEN $7 THEN $8 ELSE new_path END, new_size = CASE WHEN $9 THEN $10 ELSE new_size END, old_hash = CASE WHEN $11 THEN $12 ELSE old_hash END, old_path = CASE WHEN $13 THEN $14 ELSE old_path END, old_size = CASE WHEN $15 THEN $16 ELSE old_size END, purge_after = CASE WHEN $17 THEN $18 ELSE purge_after END, purged_at = CASE WHEN $19 THEN $20 ELSE purged_at END, trash_path = CASE WHEN $21 THEN $22 ELSE trash_path END, verify = CASE WHEN $23 THEN $24 ELSE verify END, video_id = CASE WHEN $25 THEN $26 ELSE video_id END WHERE id = $1
