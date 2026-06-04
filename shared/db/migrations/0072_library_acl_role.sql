-- +goose Up
-- +goose StatementBegin
--
-- Slot 0072 (web-pages-batch2 / Admin Library ACL) — add a `role`
-- column to `library_acl`.
--
-- Slot 0030 modelled access as presence-of-row == read. The admin ACL
-- matrix surfaces a per-cell permission *level* (read / write / admin),
-- so we promote the mapping to carry a role. Existing rows backfill to
-- 'read', which exactly preserves the slot-0030 semantics: the authz v1
-- `*.read` check still keys on row presence, and any role value implies
-- at least read. Higher levels (write/admin) are reserved for the
-- finer-grained checks the matrix now lets an admin assign.
--
-- A CHECK constraint pins the closed vocabulary so a typo'd role can't
-- be persisted. ADD COLUMN with a constant default is metadata-only on
-- Postgres (no table rewrite), so this is backfill-safe.
--
ALTER TABLE library_acl
    ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'read'
    CHECK (role IN ('read', 'write', 'admin'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE library_acl DROP COLUMN IF EXISTS role;
-- +goose StatementEnd
