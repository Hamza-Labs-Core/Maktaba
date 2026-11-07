-- name: promo_links.input_fingerprint
-- mode: read
-- scope: catalog
-- tables: series_seasons
SELECT id, membership_hash FROM series_seasons WHERE retired_at IS NULL ORDER BY id
