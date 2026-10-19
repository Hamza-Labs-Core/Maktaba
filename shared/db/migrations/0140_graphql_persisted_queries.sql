-- +goose Up
-- +goose StatementBegin
--
-- Slot 0140 (Story 7.17 AC-5) — GraphQL persisted queries. A client sends
-- the sha256 of an operation instead of its text (the Apollo protocol,
-- `extensions.persistedQuery.sha256Hash`, or `persistedQueryId`). The API
-- looks the text up here; a miss answers `PersistedQueryNotFound` and the
-- client retries with the text, which registers it.
--
-- With the `graphql.persisted_only` setting on (default off), these rows
-- are the allowlist: a non-admin caller may only run an operation stored
-- here, and only an admin adds new ones.
--
CREATE TABLE IF NOT EXISTS graphql_persisted_queries (
    sha256       TEXT        PRIMARY KEY CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    query        TEXT        NOT NULL,
    created_by   UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- the API owns the GraphQL surface; a worker never reads it
        REVOKE ALL ON graphql_persisted_queries FROM maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS graphql_persisted_queries;
-- +goose StatementEnd
