-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS audit_log (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    category      TEXT NOT NULL,
    action        TEXT NOT NULL,
    actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    target_id     TEXT,
    payload       TEXT NOT NULL DEFAULT '{}',
    ts            TEXT NOT NULL DEFAULT (datetime('now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS audit_log_actor_idx ON audit_log (actor_user_id, ts DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS audit_log_category_idx ON audit_log (category, action, ts DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS audit_log_category_idx;
DROP INDEX IF EXISTS audit_log_actor_idx;
DROP TABLE IF EXISTS audit_log;
-- +goose StatementEnd
