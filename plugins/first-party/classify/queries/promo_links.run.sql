-- name: promo_links.run
-- mode: read
-- scope: catalog
-- tables: series_seasons
SELECT id, series_id, season FROM series_seasons WHERE retired_at IS NULL
