-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0131 (Epic 31 / story 31.3): the automatic path
-- conversion's state and backups. UUID and TIMESTAMPTZ become TEXT, JSONB JSON text.
--
CREATE TABLE IF NOT EXISTS storage_canonicalize (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    state         TEXT NOT NULL DEFAULT '{}',
    request       TEXT CHECK (request IS NULL OR request IN ('run', 'revert', 'resume')),
    requested_at  TEXT,
    requested_by  TEXT,
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO storage_canonicalize (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS storage_path_backups (
    backup_id    TEXT NOT NULL,
    table_name   TEXT NOT NULL,
    column_name  TEXT NOT NULL,
    row_key      TEXT NOT NULL,
    value        TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (backup_id, table_name, column_name, row_key)
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement)
VALUES
    ('storage_canonicalize', 'Canonical share paths', '["catalog"]', 'cpu', 'media', 11, 1, 0, 0, 1, '[]', '[]', '[]', 0, 0, 0, 'auto', 'home')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'storage_canonicalize';
DROP TABLE IF EXISTS storage_path_backups;
DROP TABLE IF EXISTS storage_canonicalize;
-- +goose StatementEnd
