-- +goose Up
-- +goose StatementBegin
--
-- Slot 0160 (Epic 45, story 45.3): audit_log's category check gains 'server'. An
-- admin's change to server.toml from the web (Settings, Storage & discovery) and a
-- restart asked for there are audited under it. The list is read from the constraint
-- as it stands and 'server' put in front, so a category another slot added is kept
-- whatever the order the slots land in (as slot 0157). NOT VALID as before.
--
DO $$
DECLARE
    def text;
BEGIN
    SELECT pg_get_constraintdef(c.oid) INTO def
      FROM pg_constraint c
     WHERE c.conname = 'audit_log_category_check' AND c.conrelid = 'audit_log'::regclass;
    IF def IS NOT NULL AND position('''server''' IN def) = 0 AND position('ARRAY[' IN def) > 0 THEN
        ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_category_check;
        EXECUTE 'ALTER TABLE audit_log ADD CONSTRAINT audit_log_category_check '
            || replace(replace(def, ' NOT VALID', ''), 'ARRAY[', 'ARRAY[''server''::text, ')
            || ' NOT VALID';
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- the widened check stays: rows written under 'server' would fail a narrower one
SELECT 1;
-- +goose StatementEnd
