-- name: youtube_sources.set_source_status
-- mode: write
-- scope: catalog
-- tables: youtube_sources*
UPDATE youtube_sources SET status = $2, error = $3, updated_at = $4, last_checked_at = CASE WHEN $5 THEN $4 ELSE last_checked_at END, folder = COALESCE(folder, $6) WHERE id = $1
