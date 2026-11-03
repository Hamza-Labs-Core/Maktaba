-- +goose Up
-- +goose StatementBegin
--
-- Slot 0176 (Epic 51, story 51.03) - a joined machine's first overrides.
--
-- The home's supervisor seeds the home's desired state once, from what it runs today
-- (story 51.03; it writes the tables). A joined machine seeds nothing at home level: at
-- its first boot on this release it writes overrides for itself, only where its own
-- server.toml differs from the home ([tv] slots off on a machine whose home has them on).
-- maktaba_worker has only SELECT on component_overrides (slot 0175), so the machine goes
-- through component_override_seed(p_node, p_rows): guarded by cluster_node_guard (its own
-- node only), it inserts a row only where the machine has none for that component, so an
-- override an admin set (or cleared, which keeps the row) is never replaced. p_rows is a
-- JSON array of {id, want}; it answers how many rows it wrote, and writes one audit_log
-- row when it wrote any.
--
-- Additive: one function.
--
CREATE OR REPLACE FUNCTION component_override_seed(p_node uuid, p_rows jsonb)
RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
DECLARE
    v_n    integer;
    v_rows jsonb;
BEGIN
    PERFORM cluster_node_guard(p_node);
    IF jsonb_typeof(COALESCE(p_rows, '[]'::jsonb)) <> 'array' THEN
        RAISE EXCEPTION 'component_override_seed: rows must be a JSON array' USING ERRCODE = 'invalid_parameter_value';
    END IF;
    WITH ins AS (
        INSERT INTO component_overrides (node_id, id, want, updated_by)
        SELECT p_node, r->>'id', r->>'want', 'seed'
          FROM jsonb_array_elements(COALESCE(p_rows, '[]'::jsonb)) r
         WHERE r->>'id' IS NOT NULL AND r->>'id' <> ''
        ON CONFLICT (node_id, id) DO NOTHING
        RETURNING id, want
    )
    SELECT count(*), COALESCE(jsonb_agg(jsonb_build_object('id', id, 'want', want) ORDER BY id), '[]'::jsonb)
      INTO v_n, v_rows FROM ins;
    IF v_n > 0 THEN
        INSERT INTO audit_log (category, action, target_id, target_kind, actor_source, payload)
        VALUES ('components', 'component.seed', p_node::text, 'component', 'seed',
                jsonb_build_object('node', p_node, 'rows', v_rows));
    END IF;
    RETURN v_n;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
REVOKE EXECUTE ON FUNCTION component_override_seed(uuid, jsonb) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        GRANT EXECUTE ON FUNCTION component_override_seed(uuid, jsonb) TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS component_override_seed(uuid, jsonb);
-- +goose StatementEnd
