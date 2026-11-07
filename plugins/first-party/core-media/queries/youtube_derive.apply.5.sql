-- name: youtube_derive.apply.5
-- mode: write
-- scope: library $1
-- tables: youtube_source_decisions*
INSERT INTO youtube_source_decisions (library_id, outcome, kind, external_ids, created, clash_external_id, clash_library_id, linked_videos, matched_videos, reason, seen, decided_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT (library_id) DO UPDATE SET outcome = EXCLUDED.outcome, kind = EXCLUDED.kind, external_ids = EXCLUDED.external_ids, created = EXCLUDED.created, clash_external_id = EXCLUDED.clash_external_id, clash_library_id = EXCLUDED.clash_library_id, linked_videos = EXCLUDED.linked_videos, matched_videos = EXCLUDED.matched_videos, reason = EXCLUDED.reason, seen = EXCLUDED.seen, decided_at = EXCLUDED.decided_at
