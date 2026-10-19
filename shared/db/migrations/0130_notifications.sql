-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Runs outside a transaction because the indexes are built CONCURRENTLY.
--
-- Slot 0130 (Epic 33, story 33.13) - in-app notifications, the first general store of
-- them (README decision 8: TV changes are its first user; the review flow and other
-- features add kinds later).
--
-- notifications is one notice: kind (e.g. tv.change.ready), severity, a message key and
-- its params (the client localises them: en / ar without storing two texts), a link
-- into the web app, the audience ('admins' or one user id) and the subject it is about
-- (tv_change:<id>) so repeats can be rate limited (story 33.13 EC1). Notices persist:
-- an admin who was not signed in sees them at the next sign-in (EC2).
--
-- notification_reads is "read" per user (story 33.13 AC2).
--
-- notification_cursors is where a feed that turns rows of another table into notices
-- (the TV change events) stopped, so a restarted API neither repeats nor skips them.
--
CREATE TABLE IF NOT EXISTS notifications (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    kind       TEXT        NOT NULL,
    severity   TEXT        NOT NULL DEFAULT 'info' CHECK (severity IN ('info', 'success', 'warn', 'error')),
    title_key  TEXT        NOT NULL,
    params     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    link       TEXT        NOT NULL DEFAULT '',
    audience   TEXT        NOT NULL DEFAULT 'admins',
    subject    TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notification_reads (
    notification_id UUID        NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    user_id         TEXT        NOT NULL,
    read_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (notification_id, user_id)
);

CREATE TABLE IF NOT EXISTS notification_cursors (
    name       TEXT        PRIMARY KEY,
    position   BIGINT      NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS notifications_created_idx ON notifications (created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS notifications_subject_idx ON notifications (kind, subject, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS notification_reads_user_idx ON notification_reads (user_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- audit_log's category check (slot 0054) predates the TV categories: the TV change
-- actions (slot 0127, category tv_change) and the remote-viewing actions (Epic 32,
-- tv_remote) were refused by it. NOT VALID as in 0054: existing rows are not re-read.
ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_category_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_category_check
    CHECK (category IN (
        'auth', 'library', 'admin', 'data', 'config',
        'keys', 'device', 'security', 'integrity', 'subscription',
        'tv_change', 'tv_remote'))
    NOT VALID;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- slot 0103's default privileges gave the group role rights on new tables: the
        -- home API is the only reader and writer of notifications.
        REVOKE ALL ON notifications, notification_reads, notification_cursors FROM maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_category_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_category_check
    CHECK (category IN (
        'auth', 'library', 'admin', 'data', 'config',
        'keys', 'device', 'security', 'integrity', 'subscription'))
    NOT VALID;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS notification_cursors;
DROP TABLE IF EXISTS notification_reads;
DROP TABLE IF EXISTS notifications;
-- +goose StatementEnd
