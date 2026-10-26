-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0157 (Epic 16, story 16.8): the feature-flag overrides
-- and the beta cohorts. UUID becomes TEXT, TIMESTAMPTZ TEXT, JSONB TEXT. The shape of
-- `scope_value` for a user or a cohort is checked by the API (SQLite has no regular
-- expressions). SQLite's audit_log has no category check (slot 0054): nothing to widen.
--
CREATE TABLE IF NOT EXISTS feature_flag_overrides (
    id           TEXT PRIMARY KEY,
    flag_key     TEXT NOT NULL,
    scope        TEXT NOT NULL CHECK (scope IN ('global', 'tier', 'user', 'cohort')),
    scope_value  TEXT,
    value        TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at   TEXT,
    created_by   TEXT REFERENCES users(id) ON DELETE SET NULL,
    CHECK (
        (scope = 'global' AND scope_value IS NULL) OR
        (scope = 'tier'   AND scope_value IN ('free', 'home', 'pro')) OR
        (scope IN ('user', 'cohort') AND scope_value IS NOT NULL)
    )
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS beta_cohorts (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cohort     TEXT NOT NULL,
    joined_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (user_id, cohort)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS beta_cohorts;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS feature_flag_overrides;
-- +goose StatementEnd
