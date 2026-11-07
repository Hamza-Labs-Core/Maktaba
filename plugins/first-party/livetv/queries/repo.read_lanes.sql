-- name: repo.read_lanes
-- mode: read
-- scope: catalog
-- tables: channel_plans
WITH l AS MATERIALIZED (
                    SELECT plan->'lanes' AS lanes FROM channel_plans ORDER BY id DESC LIMIT 1
                )
                SELECT jsonb_object_agg(k, l.lanes->k) AS lanes
                FROM l, unnest($1::text[]) AS k WHERE l.lanes ? k
