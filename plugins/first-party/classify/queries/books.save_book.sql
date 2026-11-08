-- name: books.save_book
-- mode: write
-- scope: catalog
-- tables: books*
INSERT INTO books (name, version, title, url, categories, summary, checked_at) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (name) DO UPDATE SET version = EXCLUDED.version, title = EXCLUDED.title, url = EXCLUDED.url, categories = EXCLUDED.categories, summary = EXCLUDED.summary, checked_at = EXCLUDED.checked_at
