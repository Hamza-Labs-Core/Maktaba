-- name: promo_links.run.2
-- mode: write
-- scope: library $1
-- tables: promo_links*
DELETE FROM promo_links WHERE library_id = $1
