-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0181 (Epic 51, story 51.19): components.licensed and
-- components.licensed_until, what the home's licence allows. A change of only those two
-- moves the revision, not the row's generation.
--
ALTER TABLE components ADD COLUMN IF NOT EXISTS licensed INTEGER;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE components ADD COLUMN IF NOT EXISTS licensed_until TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS components_generation_upd;
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0178's trigger, except for an update that changed only what the licence allows
CREATE TRIGGER IF NOT EXISTS components_generation_upd
    AFTER UPDATE ON components
    FOR EACH ROW
    WHEN NEW.generation = OLD.generation
     AND NOT ((NEW.licensed IS NOT OLD.licensed OR NEW.licensed_until IS NOT OLD.licensed_until)
              AND NEW.want IS OLD.want AND NEW.hold_by IS OLD.hold_by
              AND NEW.hold_at IS OLD.hold_at AND NEW.updated_by IS OLD.updated_by)
BEGIN
    UPDATE components SET generation = OLD.generation + 1,
        updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
     WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
-- that update still moves the revision once
CREATE TRIGGER IF NOT EXISTS components_revision_licence
    AFTER UPDATE OF licensed, licensed_until ON components
    FOR EACH ROW
    WHEN NEW.generation = OLD.generation
     AND (NEW.licensed IS NOT OLD.licensed OR NEW.licensed_until IS NOT OLD.licensed_until)
     AND NEW.want IS OLD.want AND NEW.hold_by IS OLD.hold_by
     AND NEW.hold_at IS OLD.hold_at AND NEW.updated_by IS OLD.updated_by
BEGIN
    UPDATE component_meta SET revision = revision + 1 WHERE singleton = 1;
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS components_revision_licence;
-- +goose StatementEnd
