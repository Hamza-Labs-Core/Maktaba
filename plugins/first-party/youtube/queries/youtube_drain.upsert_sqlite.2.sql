-- name: youtube_drain.upsert_sqlite.2
-- mode: write
-- scope: library $2
-- tables: videos*
INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime, last_seen_at, state) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'discovered')
