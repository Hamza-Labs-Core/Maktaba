-- name: store.save_candidate.3
-- mode: write
-- scope: library $1
-- tables: videos*
INSERT INTO videos
       (library_id, content_hash, path, filename, size_bytes, mtime,
        last_seen_at, state)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'discovered')
ON CONFLICT (library_id, content_hash) DO UPDATE SET
    path         = EXCLUDED.path,
    filename     = EXCLUDED.filename,
    size_bytes   = EXCLUDED.size_bytes,
    mtime        = EXCLUDED.mtime,
    last_seen_at = EXCLUDED.last_seen_at,
    updated_at   = now()
RETURNING id, (xmax = 0) AS inserted
