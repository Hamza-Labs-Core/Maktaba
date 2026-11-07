-- name: build.insert_release
-- mode: write
-- scope: catalog
-- tables: tv_releases*
INSERT INTO tv_releases (id, digest, kinds, sched_gen, streaming_version, streaming_sha256, settings, lineup_digest, base_release_id, created_at, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
