-- +goose Up
-- System metadata tables: a single-row settings table, plus a view
-- that surfaces the current schema version for /readyz and ops tooling.
CREATE TABLE IF NOT EXISTS cloud_system (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    installed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    region       TEXT        NOT NULL DEFAULT 'fsn1',
    build_commit TEXT
);

INSERT INTO cloud_system (id) VALUES (1) ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS cloud_system;
