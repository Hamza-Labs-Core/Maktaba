-- name: leftovers.load_shows
-- mode: read
-- scope: catalog
-- tables: libraries, series, series_seasons
SELECT s.id AS id, COALESCE(s.name_override, s.name) AS name, s.library_id AS lib, COALESCE(l.name, '') AS library, ss.season AS season FROM series s JOIN series_seasons ss ON ss.series_id = s.id AND ss.retired_at IS NULL AND ss.kind <> 'clips' LEFT JOIN libraries l ON l.id = s.library_id ORDER BY s.id, ss.season
