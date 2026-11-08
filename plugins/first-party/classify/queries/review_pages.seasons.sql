-- name: review_pages.seasons
-- mode: read
-- scope: library $1
-- tables: series, series_seasons
SELECT ss.id AS id, s.name AS series, COALESCE(s.name_override, s.name) AS shown, ss.season AS season, ss.kind AS kind, ss.name AS name, ss.first_air AS first_air, ss.last_air AS last_air, ss.membership_hash AS membership FROM series_seasons ss JOIN series s ON s.id = ss.series_id WHERE ss.library_id = $1 AND ss.retired_at IS NULL ORDER BY s.name, ss.season
