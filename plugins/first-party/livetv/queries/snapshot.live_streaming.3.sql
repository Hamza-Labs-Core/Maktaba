-- name: snapshot.live_streaming.3
-- mode: read
-- scope: catalog
-- tables: tv_releases
SELECT streaming_version, streaming_sha256 FROM tv_releases WHERE COALESCE(streaming_sha256, '') <> '' ORDER BY created_at DESC
