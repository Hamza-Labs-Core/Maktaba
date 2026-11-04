-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0178 (Epic 51, story 51.02): the four component tables,
-- the triggers that move generation and component_meta.revision, and the
-- component_catchup stage row. SQLite has no NOTIFY (runners poll the revision every
-- 30 s), no roles and no SECURITY DEFINER functions: a single-process SQLite rig is the
-- home, and the stores write the tables directly. Its audit_log has no category check
-- (slot 0054): nothing to widen. Timestamps are text, JSON is text.
--
CREATE TABLE IF NOT EXISTS components (
    id          TEXT PRIMARY KEY,
    want        TEXT NOT NULL CHECK (want IN ('absent', 'installed', 'enabled')),
    hold_by     TEXT,
    hold_at     TEXT,
    generation  INTEGER NOT NULL DEFAULT 1,
    updated_by  TEXT NOT NULL,
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK ((hold_at IS NULL) = (hold_by IS NULL))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS component_overrides (
    node_id     TEXT NOT NULL REFERENCES cluster_nodes(id) ON DELETE CASCADE,
    id          TEXT NOT NULL,
    want        TEXT CHECK (want IN ('absent', 'installed', 'enabled')),
    hold_by     TEXT,
    hold_at     TEXT,
    generation  INTEGER NOT NULL DEFAULT 1,
    updated_by  TEXT NOT NULL,
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (node_id, id),
    CHECK ((hold_at IS NULL) = (hold_by IS NULL))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS component_status (
    machine             TEXT NOT NULL,
    id                  TEXT NOT NULL,
    part                TEXT NOT NULL,
    phase               TEXT NOT NULL,
    conditions          TEXT NOT NULL DEFAULT '[]',
    observed_generation INTEGER NOT NULL DEFAULT 0,
    version             TEXT,
    detail              TEXT NOT NULL DEFAULT '{}',
    since               TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (machine, id, part)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS component_meta (
    singleton      INTEGER PRIMARY KEY DEFAULT 1 CHECK (singleton = 1),
    revision       INTEGER NOT NULL DEFAULT 0,
    bundle         TEXT CHECK (bundle IN ('full', 'core', 'worker', 'custom')),
    seeded_at      TEXT,
    seeded_release TEXT,
    toml_imported  TEXT NOT NULL DEFAULT '{}'
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO component_meta (singleton) VALUES (1) ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
-- an update is a new generation: the first trigger moves it when the writer did not, the
-- second moves the revision once per changed row (the first trigger's own update fires
-- it, an update that moved the generation itself fires it directly)
CREATE TRIGGER IF NOT EXISTS components_generation_upd
    AFTER UPDATE ON components
    FOR EACH ROW
    WHEN NEW.generation = OLD.generation
BEGIN
    UPDATE components SET generation = OLD.generation + 1,
        updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
     WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS components_revision_upd
    AFTER UPDATE ON components
    FOR EACH ROW
    WHEN NEW.generation <> OLD.generation
BEGIN
    UPDATE component_meta SET revision = revision + 1 WHERE singleton = 1;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS components_revision_ins
    AFTER INSERT ON components
    FOR EACH ROW
BEGIN
    UPDATE component_meta SET revision = revision + 1 WHERE singleton = 1;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS components_revision_del
    AFTER DELETE ON components
    FOR EACH ROW
BEGIN
    UPDATE component_meta SET revision = revision + 1 WHERE singleton = 1;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS component_overrides_generation_upd
    AFTER UPDATE ON component_overrides
    FOR EACH ROW
    WHEN NEW.generation = OLD.generation
BEGIN
    UPDATE component_overrides SET generation = OLD.generation + 1,
        updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
     WHERE node_id = NEW.node_id AND id = NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS component_overrides_revision_upd
    AFTER UPDATE ON component_overrides
    FOR EACH ROW
    WHEN NEW.generation <> OLD.generation
BEGIN
    UPDATE component_meta SET revision = revision + 1 WHERE singleton = 1;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS component_overrides_revision_ins
    AFTER INSERT ON component_overrides
    FOR EACH ROW
BEGIN
    UPDATE component_meta SET revision = revision + 1 WHERE singleton = 1;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS component_overrides_revision_del
    AFTER DELETE ON component_overrides
    FOR EACH ROW
BEGIN
    UPDATE component_meta SET revision = revision + 1 WHERE singleton = 1;
END;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled)
VALUES
    ('component_catchup', 'Catch up', '["catalog"]', 'cpu', 'media', 99, 1, 0, 0, 3,
     '[]', '[]', '[]', 0, 1, 0, 'auto', 'home', 1)
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'component_catchup'
   AND NOT EXISTS (SELECT 1 FROM processing_jobs j WHERE j.stage = 'component_catchup');
DROP TRIGGER IF EXISTS component_overrides_revision_del;
DROP TRIGGER IF EXISTS component_overrides_revision_ins;
DROP TRIGGER IF EXISTS component_overrides_revision_upd;
DROP TRIGGER IF EXISTS component_overrides_generation_upd;
DROP TRIGGER IF EXISTS components_revision_del;
DROP TRIGGER IF EXISTS components_revision_ins;
DROP TRIGGER IF EXISTS components_revision_upd;
DROP TRIGGER IF EXISTS components_generation_upd;
DROP TABLE IF EXISTS component_meta;
DROP TABLE IF EXISTS component_status;
DROP TABLE IF EXISTS component_overrides;
DROP TABLE IF EXISTS components;
-- +goose StatementEnd
