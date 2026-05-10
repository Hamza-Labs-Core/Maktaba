-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0050 (Story 10.2 / plan-10-02) — `web_sessions` table.
--
-- Cookie-based session store for the web (SPA) surface. The cookie
-- `mkt_sess` carries the row id; the CSRF double-submit token
-- `mkt_csrf` is stored here so the server can verify both halves on
-- mutating requests. Schema mirrors README.md §"web_sessions".
--
-- Indexes:
--   * `web_sessions_user_active`: fast "list active sessions for this
--     user" (admin and logout-all paths).
--   * `web_sessions_reaper`: oldest-first scan for the expiry reaper.
--
CREATE TABLE IF NOT EXISTS web_sessions (
    id            UUID         PRIMARY KEY,
    user_id       UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token    TEXT         NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ  NOT NULL,
    ip            INET,
    user_agent    TEXT,
    revoked_at    TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS web_sessions_user_active
    ON web_sessions (user_id) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS web_sessions_reaper
    ON web_sessions (expires_at) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS web_sessions_reaper;
DROP INDEX IF EXISTS web_sessions_user_active;
DROP TABLE IF EXISTS web_sessions;
-- +goose StatementEnd
