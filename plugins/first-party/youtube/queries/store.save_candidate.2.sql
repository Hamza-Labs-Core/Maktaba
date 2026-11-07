-- name: store.save_candidate.2
-- mode: write
-- scope: catalog
-- tables: videos*
UPDATE videos SET last_seen_at = $2 WHERE id = $1
