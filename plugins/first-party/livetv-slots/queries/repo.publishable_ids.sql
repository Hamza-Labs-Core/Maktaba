-- name: repo.publishable_ids
-- mode: read
-- scope: catalog
-- tables: 
SELECT channel_publishable_only($1::uuid) AS limited
