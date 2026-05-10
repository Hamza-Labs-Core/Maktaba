-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0030 (Story 10.13 / plan-10-13) — `library_acl` mapping table.
--
-- One row per (user, library) pair the user can read. v1 only models a
-- single read role; v2 may add `role TEXT` for finer scopes. Until then
-- presence-of-row == read-access.
--
-- The Authz interface (Story 10.13 AC-1) enforces:
--   * `*.read` on a library_id → row must exist for (user_id,
--      library_id), or the caller is_admin, or single-user mode is on.
--   * `library.*` (admin scope) → caller must be admin regardless of
--      ACL rows.
--
-- Snapshotted into JWT `lib[]` claim at issue time (Story 10.13 AC-5).
-- Mid-session ACL revocation has up-to-15-min staleness; logout-all
-- forces immediate refresh (Story 10.5).
--
CREATE TABLE IF NOT EXISTS library_acl (
    user_id     UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id  UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    granted_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, library_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS library_acl_library_idx
    ON library_acl (library_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS library_acl_library_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS library_acl;
-- +goose StatementEnd
