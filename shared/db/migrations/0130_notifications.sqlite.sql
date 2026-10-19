-- +goose Up
-- +goose StatementBegin
--
-- Slot 0130 (Epic 33, story 33.13), SQLite twin: in-app notifications with TEXT ids,
-- timestamps and JSON. SQLite's audit_log has no category check (slot 0054), so there
-- is nothing to widen.
--
CREATE TABLE IF NOT EXISTS notifications (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,
    severity   TEXT NOT NULL DEFAULT 'info' CHECK (severity IN ('info', 'success', 'warn', 'error')),
    title_key  TEXT NOT NULL,
    params     TEXT NOT NULL DEFAULT '{}',
    link       TEXT NOT NULL DEFAULT '',
    audience   TEXT NOT NULL DEFAULT 'admins',
    subject    TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS notifications_created_idx ON notifications (created_at DESC);
CREATE INDEX IF NOT EXISTS notifications_subject_idx ON notifications (kind, subject, created_at DESC);

CREATE TABLE IF NOT EXISTS notification_reads (
    notification_id TEXT NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL,
    read_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (notification_id, user_id)
);

CREATE INDEX IF NOT EXISTS notification_reads_user_idx ON notification_reads (user_id);

CREATE TABLE IF NOT EXISTS notification_cursors (
    name       TEXT    PRIMARY KEY,
    position   INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS notification_cursors;
DROP TABLE IF EXISTS notification_reads;
DROP TABLE IF EXISTS notifications;
-- +goose StatementEnd
