-- name: repo.publishable_ids.2
-- mode: read
-- scope: catalog
-- tables: 
SELECT v AS id FROM unnest($1::uuid[]) AS v WHERE video_publishable_reason(v) IS NOT NULL
