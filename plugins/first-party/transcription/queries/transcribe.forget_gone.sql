-- name: transcribe.forget_gone
-- mode: write
-- scope: catalog
-- tables: videos, relisten_attempts*
DELETE FROM relisten_attempts WHERE video_id IN (SELECT v.id FROM videos v WHERE (v.deleted_at IS NOT NULL OR v.missing_confirmed_at IS NOT NULL OR v.state IN ('missing', 'superseded'))) RETURNING video_id
