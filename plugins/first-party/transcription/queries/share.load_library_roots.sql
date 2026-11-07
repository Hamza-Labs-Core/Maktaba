-- name: share.load_library_roots
-- mode: read
-- scope: catalog
-- tables: libraries, library_roots, videos
SELECT l.id AS library_id, lr.path AS path, (SELECT count(*) FROM videos v WHERE v.library_id = l.id AND v.deleted_at IS NULL) AS videos FROM library_roots lr JOIN libraries l ON l.id = lr.library_id WHERE l.deleted_at IS NULL ORDER BY l.id, lr.path
