-- name: snapshot.live_streaming.2
-- mode: read
-- scope: catalog
-- tables: tv_releases, tv_routing
SELECT r.streaming_version, r.streaming_sha256 FROM tv_routing t JOIN tv_releases r ON r.id = t.live_release_id WHERE t.scope = 'cluster'
