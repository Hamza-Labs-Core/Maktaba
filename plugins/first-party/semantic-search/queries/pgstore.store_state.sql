-- name: pgstore.store_state
-- mode: read
-- scope: catalog
-- tables: semantic_store_state
SELECT semantic_store_ready() AS ready, (SELECT store FROM semantic_store_state LIMIT 1) AS store
