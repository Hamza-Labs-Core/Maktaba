-- +goose Up
-- +goose StatementBegin
--
-- Slot 0174 (Epic 51, story 51.02) - desired and actual state of the components.
--
-- The component catalog (shared/components/catalog.toml) says what parts Maktaba has.
-- These tables say which should run and what each machine is really doing, kept apart
-- (README §4.3, H1, H13):
--
--   components            desired state, one row per component per home. The API writes
--                         it (and the supervisor once, when it seeds: 51.03). want is
--                         absent / installed / enabled; a hold ({hold_by, hold_at}) is a
--                         stop that lasts until an admin starts it again (no end time).
--   component_overrides   per-machine exceptions (51.12). A NULL want follows the home.
--                         Clear an override by setting want and hold to NULL, not by
--                         deleting it, so the merged generation only goes up.
--   component_status      actual state, one row per (machine, component, part). Only that
--                         part writes it: the home's own processes directly, a joined
--                         machine only through component_status_report().
--   component_meta        one row: the change counter every runner polls, the bundle and
--                         the seed marker (51.03).
--
-- The change signal: every insert, update or delete of components or
-- component_overrides bumps the row's generation (on update) and
-- component_meta.revision, and sends NOTIFY components_changed with
-- {"revision", "id", "node"}. The trigger is the only writer of revision. Runners LISTEN
-- and also poll the revision every 30 s.
--
-- maktaba_worker (the machines' group) keeps SELECT on the four tables and loses its
-- writes (0103 granted them on every table): a machine reads its merged desired state
-- through component_desired_for() and reports through component_status_report(), both
-- guarded by cluster_node_guard() (its own node only).
--
-- Also: audit_log's category check gains 'components', and the component_catchup stage
-- (51.06) has its job_stages row, so SQL that reads job_stages before a worker's graph
-- sync knows it. The row is hidden (no queue lane shows it) until 51.06's StageDef owns it.
--
-- Additive: four tables, three functions, two triggers, one stage row.
--
CREATE TABLE IF NOT EXISTS components (
    id          text PRIMARY KEY,
    want        text NOT NULL CHECK (want IN ('absent', 'installed', 'enabled')),
    hold_by     text,
    hold_at     timestamptz,
    generation  bigint NOT NULL DEFAULT 1,
    updated_by  text NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CHECK ((hold_at IS NULL) = (hold_by IS NULL))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS component_overrides (
    node_id     uuid NOT NULL REFERENCES cluster_nodes(id) ON DELETE CASCADE,
    id          text NOT NULL,
    want        text CHECK (want IN ('absent', 'installed', 'enabled')),
    hold_by     text,
    hold_at     timestamptz,
    generation  bigint NOT NULL DEFAULT 1,
    updated_by  text NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, id),
    CHECK ((hold_at IS NULL) = (hold_by IS NULL))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS component_status (
    machine             text NOT NULL,
    id                  text NOT NULL,
    part                text NOT NULL,
    phase               text NOT NULL,
    conditions          jsonb NOT NULL DEFAULT '[]'::jsonb,
    observed_generation bigint NOT NULL DEFAULT 0,
    version             text,
    detail              jsonb NOT NULL DEFAULT '{}'::jsonb,
    since               timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (machine, id, part)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS component_meta (
    singleton      boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    revision       bigint NOT NULL DEFAULT 0,
    bundle         text CHECK (bundle IN ('full', 'core', 'worker', 'custom')),
    seeded_at      timestamptz,
    seeded_release text,
    toml_imported  jsonb NOT NULL DEFAULT '{}'::jsonb
);
INSERT INTO component_meta (singleton) VALUES (true) ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
-- an update of a desired row is a new generation, whatever the writer set
CREATE OR REPLACE FUNCTION components_next_generation() RETURNS trigger
LANGUAGE plpgsql AS $f$
BEGIN
    NEW.generation := OLD.generation + 1;
    NEW.updated_at := now();
    RETURN NEW;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- every change of a desired row moves the revision and says so on components_changed
CREATE OR REPLACE FUNCTION components_changed() RETURNS trigger
LANGUAGE plpgsql AS $f$
DECLARE
    v_rev  bigint;
    v_id   text;
    v_node uuid;
BEGIN
    IF TG_OP = 'DELETE' THEN
        v_id := OLD.id;
        IF TG_TABLE_NAME = 'component_overrides' THEN
            v_node := OLD.node_id;
        END IF;
    ELSE
        v_id := NEW.id;
        IF TG_TABLE_NAME = 'component_overrides' THEN
            v_node := NEW.node_id;
        END IF;
    END IF;
    UPDATE component_meta SET revision = revision + 1 WHERE singleton RETURNING revision INTO v_rev;
    PERFORM pg_notify('components_changed',
        json_build_object('revision', v_rev, 'id', v_id, 'node', v_node)::text);
    RETURN NULL;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER components_generation_trg
    BEFORE UPDATE ON components
    FOR EACH ROW EXECUTE FUNCTION components_next_generation();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER component_overrides_generation_trg
    BEFORE UPDATE ON component_overrides
    FOR EACH ROW EXECUTE FUNCTION components_next_generation();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER components_changed_trg
    AFTER INSERT OR UPDATE OR DELETE ON components
    FOR EACH ROW EXECUTE FUNCTION components_changed();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER component_overrides_changed_trg
    AFTER INSERT OR UPDATE OR DELETE ON component_overrides
    FOR EACH ROW EXECUTE FUNCTION components_changed();
-- +goose StatementEnd

-- +goose StatementBegin
-- A machine's report of one of its parts (supervisor, api, streaming, pipeline,
-- pipeline/stt-1, ...): p_rows is a JSON array of {id, phase, conditions,
-- observed_generation, version, detail}. The rows land with machine = p_node; rows of
-- that machine and part not in p_rows are removed (the part no longer runs them). since
-- moves only when the phase changes; both times are the database's, not the machine's.
CREATE OR REPLACE FUNCTION component_status_report(p_node uuid, p_part text, p_rows jsonb)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    PERFORM cluster_node_guard(p_node);
    IF p_part IS NULL OR p_part = '' THEN
        RAISE EXCEPTION 'component_status_report: a part is required' USING ERRCODE = 'invalid_parameter_value';
    END IF;
    IF jsonb_typeof(COALESCE(p_rows, '[]'::jsonb)) <> 'array' THEN
        RAISE EXCEPTION 'component_status_report: rows must be a JSON array' USING ERRCODE = 'invalid_parameter_value';
    END IF;
    INSERT INTO component_status AS s
        (machine, id, part, phase, conditions, observed_generation, version, detail, since, updated_at)
    SELECT p_node::text, r->>'id', p_part, r->>'phase',
           COALESCE(r->'conditions', '[]'::jsonb),
           COALESCE((r->>'observed_generation')::bigint, 0),
           r->>'version',
           CASE WHEN octet_length(COALESCE(r->'detail', '{}'::jsonb)::text) > 4096
                THEN '{"truncated": true}'::jsonb
                ELSE COALESCE(r->'detail', '{}'::jsonb) END,
           now(), now()
      FROM jsonb_array_elements(COALESCE(p_rows, '[]'::jsonb)) r
     WHERE r->>'id' IS NOT NULL AND r->>'phase' IS NOT NULL
    ON CONFLICT (machine, id, part) DO UPDATE SET
        phase = EXCLUDED.phase,
        conditions = EXCLUDED.conditions,
        observed_generation = EXCLUDED.observed_generation,
        version = EXCLUDED.version,
        detail = EXCLUDED.detail,
        since = CASE WHEN s.phase IS DISTINCT FROM EXCLUDED.phase THEN now() ELSE s.since END,
        updated_at = now();
    DELETE FROM component_status s
     WHERE s.machine = p_node::text AND s.part = p_part
       AND s.id NOT IN (SELECT r->>'id' FROM jsonb_array_elements(COALESCE(p_rows, '[]'::jsonb)) r
                         WHERE r->>'id' IS NOT NULL);
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The home's desired rows with p_node's overrides applied: a node reads one answer and
-- never computes the merge itself. generation is the home row's plus the override's, so
-- it moves when either does.
CREATE OR REPLACE FUNCTION component_desired_for(p_node uuid)
RETURNS TABLE (id text, want text, hold_by text, hold_at timestamptz, generation bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    PERFORM cluster_node_guard(p_node);
    RETURN QUERY
    SELECT COALESCE(c.id, o.id),
           COALESCE(o.want, c.want),
           CASE WHEN o.hold_at IS NOT NULL THEN o.hold_by ELSE c.hold_by END,
           CASE WHEN o.hold_at IS NOT NULL THEN o.hold_at ELSE c.hold_at END,
           COALESCE(c.generation, 0) + COALESCE(o.generation, 0)
      FROM components c
      FULL OUTER JOIN (SELECT * FROM component_overrides x WHERE x.node_id = p_node) o
        ON o.id = c.id
     WHERE COALESCE(o.want, c.want) IS NOT NULL
     ORDER BY 1;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- callable by the machines' group (and the owner), not by every role; the machines'
-- group reads the tables and writes none of them
REVOKE EXECUTE ON FUNCTION component_status_report(uuid, text, jsonb),
    component_desired_for(uuid) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        GRANT EXECUTE ON FUNCTION component_status_report(uuid, text, jsonb),
            component_desired_for(uuid) TO maktaba_worker;
        REVOKE ALL ON components, component_overrides, component_status, component_meta
            FROM maktaba_worker;
        GRANT SELECT ON components, component_overrides, component_status, component_meta
            TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- audit_log's category check gains 'components' (the list read from the constraint as
-- it stands, as slot 0160)
DO $$
DECLARE
    def text;
BEGIN
    SELECT pg_get_constraintdef(c.oid) INTO def
      FROM pg_constraint c
     WHERE c.conname = 'audit_log_category_check' AND c.conrelid = 'audit_log'::regclass;
    IF def IS NOT NULL AND position('''components''' IN def) = 0 AND position('ARRAY[' IN def) > 0 THEN
        ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_category_check;
        EXECUTE 'ALTER TABLE audit_log ADD CONSTRAINT audit_log_category_check '
            || replace(replace(def, ' NOT VALID', ''), 'ARRAY[', 'ARRAY[''components''::text, ')
            || ' NOT VALID';
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- the catch-up after a component is enabled again (51.06 adds its StageDef; the graph
-- sync then owns the row)
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled)
VALUES
    ('component_catchup', 'Catch up', ARRAY['catalog']::text[], 'cpu', 'media', 99, 1, 0, 0, 3,
     '[]'::jsonb, '{}'::text[], '{}'::text[], false, true, false, 'auto', 'home', true)
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages s WHERE s.stage = 'component_catchup'
   AND NOT EXISTS (SELECT 1 FROM processing_jobs j WHERE j.stage = s.stage);
DROP FUNCTION IF EXISTS component_desired_for(uuid);
DROP FUNCTION IF EXISTS component_status_report(uuid, text, jsonb);
DROP TABLE IF EXISTS component_meta;
DROP TABLE IF EXISTS component_status;
DROP TABLE IF EXISTS component_overrides;
DROP TABLE IF EXISTS components;
DROP FUNCTION IF EXISTS components_changed();
DROP FUNCTION IF EXISTS components_next_generation();
-- +goose StatementEnd
