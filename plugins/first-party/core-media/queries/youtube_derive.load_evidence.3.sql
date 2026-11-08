-- name: youtube_derive.load_evidence.3
-- mode: read
-- scope: catalog
-- tables: youtube_sources
SELECT library_id, kind, external_id FROM youtube_sources
