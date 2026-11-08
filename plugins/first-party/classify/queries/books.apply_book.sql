-- name: books.apply_book
-- mode: write
-- scope: season $1
-- tables: series_books*
INSERT INTO series_books (season_id, name, chapter, fields) VALUES ($1,$2,$3,$4) ON CONFLICT (season_id) DO UPDATE SET name = EXCLUDED.name, chapter = EXCLUDED.chapter, fields = EXCLUDED.fields
