-- name: repo.attributions
-- mode: read
-- scope: catalog
-- tables: 
SELECT v AS id, video_attribution(v) AS a FROM unnest($1::uuid[]) AS v WHERE video_attribution(v) IS NOT NULL
