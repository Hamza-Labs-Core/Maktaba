-- name: promo_links.run.3
-- mode: write
-- scope: subject $1
-- tables: promo_links*
INSERT INTO promo_links (video_id, library_id, series_id, season_id, season, score, evidence, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
