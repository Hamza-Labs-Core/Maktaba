-- name: detect.apply_changes.5
-- mode: write
-- scope: season $1
-- tables: series_seasons*
UPDATE series_seasons SET retired_at = $2, updated_at = $2 WHERE id = $1
