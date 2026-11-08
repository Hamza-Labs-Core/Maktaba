-- name: check_tags.run.2
-- mode: write
-- scope: catalog
-- tables: tag_checks*
INSERT INTO tag_checks (season_id, input_hash, model, answer, applied_hash, checked_at) VALUES ($1,$2,$3,$4,NULL,$5) ON CONFLICT (season_id) DO UPDATE SET input_hash = EXCLUDED.input_hash, model = EXCLUDED.model, answer = EXCLUDED.answer, applied_hash = NULL, checked_at = EXCLUDED.checked_at
