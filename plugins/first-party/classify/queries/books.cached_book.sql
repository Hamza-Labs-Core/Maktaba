-- name: books.cached_book
-- mode: read
-- scope: catalog
-- tables: books
SELECT version, title, url, categories, summary, checked_at FROM books WHERE name = $1
