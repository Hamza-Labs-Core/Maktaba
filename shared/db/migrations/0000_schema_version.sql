-- +goose Up
-- +goose StatementBegin
--
-- Slot 0000 (Story 22.4) — smoke-test migration that proves the goose
-- runner is wired up and the migrations directory is reachable. Real
-- schema starts at slot 0001 (see MANIFEST.md).
--
-- The `maktaba_schema_version` row records when this codebase first
-- bootstrapped its database; ops uses it to confirm a fresh DB has
-- actually been migrated rather than relying on the goose meta table
-- alone.
--
CREATE TABLE IF NOT EXISTS maktaba_schema_version (
    id          SMALLINT PRIMARY KEY,
    bootstrapped_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    notes       TEXT NOT NULL DEFAULT ''
);

INSERT INTO maktaba_schema_version (id, notes)
VALUES (1, 'goose runner bootstrapped (Story 22.4)')
ON CONFLICT (id) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS maktaba_schema_version;
-- +goose StatementEnd
