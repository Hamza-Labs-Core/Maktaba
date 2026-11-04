-- +goose Up
-- +goose StatementBegin
-- SQLite has no roles and no functions: a single-process SQLite rig is the home, and the
-- store writes a machine's first overrides into component_overrides directly
-- (store.SeedOverrides). This file exists so the slot is fully populated; the schema
-- delta is empty on SQLite.
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
