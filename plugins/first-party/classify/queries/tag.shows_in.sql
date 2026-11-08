-- name: tag.shows_in
-- mode: read
-- scope: library $1
-- tables: series_seasons
SELECT DISTINCT series_id FROM series_seasons WHERE library_id = $1 AND retired_at IS NULL AND CAST(metadata AS TEXT) LIKE $2
