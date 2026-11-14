-- +goose Up
-- Story 50.25: the broadcast kill switch. Expand only: three new tables, nothing the running
-- release reads changes.

-- broadcast_revocations: each entry a service admin added. lifted_at set: no longer in the list
-- (only a later list version without it lifts it at the edge and at homes). A programme entry
-- has until_at and leaves the signed list by itself once it passes.
CREATE TABLE IF NOT EXISTS broadcast_revocations (
    id         BIGSERIAL   PRIMARY KEY,
    kind       TEXT        NOT NULL CHECK (kind IN ('channel', 'programme', 'publisher', 'home', 'global')),
    ref        TEXT        NOT NULL DEFAULT '' CHECK (length(ref) <= 128),
    action     TEXT        NOT NULL CHECK (action IN ('slate', 'off_air', 'suspend', 'terminate')),
    reason     TEXT        NOT NULL CHECK (reason IN ('notice', 'report', 'incident', 'abuse', 'repeat_infringer')),
    note       TEXT        NOT NULL DEFAULT '' CHECK (length(note) <= 500),
    until_at   TIMESTAMPTZ,
    added_by   TEXT        NOT NULL,
    added_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    lifted_by  TEXT,
    lifted_at  TIMESTAMPTZ,
    lift_note  TEXT        NOT NULL DEFAULT '' CHECK (length(lift_note) <= 500)
);
CREATE INDEX IF NOT EXISTS broadcast_revocations_live ON broadcast_revocations (id) WHERE lifted_at IS NULL;

-- broadcast_revocation_list: the one row holding the list's version, which only goes up (each
-- add and lift moves it in the same transaction).
CREATE TABLE IF NOT EXISTS broadcast_revocation_list (
    singleton  BOOLEAN     PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    version    BIGINT      NOT NULL DEFAULT 0 CHECK (version >= 0),
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO broadcast_revocation_list (singleton) VALUES (TRUE) ON CONFLICT DO NOTHING;

-- broadcast_revocation_audit: every add and lift, who did it (the staff account), when, why,
-- and the list version it made. Append-only.
CREATE TABLE IF NOT EXISTS broadcast_revocation_audit (
    id            BIGSERIAL   PRIMARY KEY,
    revocation_id BIGINT      NOT NULL REFERENCES broadcast_revocations(id),
    action        TEXT        NOT NULL CHECK (action IN ('add', 'lift')),
    version       BIGINT      NOT NULL,
    actor_id      TEXT        NOT NULL,
    actor_email   TEXT        NOT NULL DEFAULT '',
    note          TEXT        NOT NULL DEFAULT '',
    at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS broadcast_revocation_audit_at ON broadcast_revocation_audit (at DESC);

-- +goose Down
DROP TABLE IF EXISTS broadcast_revocation_audit;
DROP TABLE IF EXISTS broadcast_revocation_list;
DROP TABLE IF EXISTS broadcast_revocations;
