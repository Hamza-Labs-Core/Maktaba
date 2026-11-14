-- +goose Up
-- Story 50.24: takedown, reports and repeat infringers. Expand only: new tables and one nullable
-- column; nothing the running release reads changes. The tables are named moderation_* because
-- broadcast_notices already holds 50.20's usage notices.

-- the edge's opaque id of a published channel (broadcast.ChannelID), recorded when the home asks
-- for its ingest token with the channel's cloud id: a takedown's programme slate names it
ALTER TABLE broadcast_channels ADD COLUMN IF NOT EXISTS edge_channel TEXT;

-- moderation_notices: each copyright notice (§ 512(c)(3) and DSA Art. 16), from the public form
-- or entered by a service admin from an email to the agent. A notice missing an element is
-- answered with what is missing and never stored.
CREATE TABLE IF NOT EXISTS moderation_notices (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    source           TEXT        NOT NULL CHECK (source IN ('form', 'email')),
    status           TEXT        NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'accepted', 'rejected', 'withdrawn')),
    claimant_name    TEXT        NOT NULL,
    claimant_email   TEXT        NOT NULL,
    claimant_address TEXT        NOT NULL DEFAULT '',
    claimant_phone   TEXT        NOT NULL DEFAULT '',
    work             TEXT        NOT NULL,
    urls             TEXT[]      NOT NULL,
    programme        TEXT        NOT NULL DEFAULT '',
    at_time          TIMESTAMPTZ,
    explanation      TEXT        NOT NULL,
    signature        TEXT        NOT NULL,
    privacy          BOOLEAN     NOT NULL DEFAULT false,
    urgent           BOOLEAN     NOT NULL DEFAULT false,
    -- what the URL names (resolved on arrival; NULL: no published channel matched)
    server_id        UUID,
    channel_id       UUID,
    publisher_id     UUID,
    ip_hash          TEXT        NOT NULL DEFAULT '',
    received_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    entered_by       TEXT        NOT NULL DEFAULT '',
    decided_at       TIMESTAMPTZ,
    decided_by       TEXT        NOT NULL DEFAULT '',
    decision_note    TEXT        NOT NULL DEFAULT '',
    unfounded        BOOLEAN     NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS moderation_notices_open ON moderation_notices (received_at) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS moderation_notices_claimant ON moderation_notices (lower(claimant_email));
CREATE INDEX IF NOT EXISTS moderation_notices_server ON moderation_notices (server_id);

-- moderation_reports: what viewers report from a watch surface (anonymous; the email optional).
CREATE TABLE IF NOT EXISTS moderation_reports (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    category      TEXT        NOT NULL CHECK (category IN ('copyright', 'illegal', 'abuse', 'other')),
    about         TEXT        NOT NULL DEFAULT 'content' CHECK (about IN ('content', 'publisher')),
    status        TEXT        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'actioned', 'dismissed')),
    channel_url   TEXT        NOT NULL,
    server_id     UUID,
    channel_id    UUID,
    publisher_id  UUID,
    programme     TEXT        NOT NULL DEFAULT '',
    at_time       TIMESTAMPTZ NOT NULL,
    email         TEXT        NOT NULL DEFAULT '',
    body          TEXT        NOT NULL DEFAULT '',
    urgent        BOOLEAN     NOT NULL DEFAULT false,
    ip_hash       TEXT        NOT NULL DEFAULT '',
    received_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at    TIMESTAMPTZ,
    decided_by    TEXT        NOT NULL DEFAULT '',
    decision_note TEXT        NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS moderation_reports_open ON moderation_reports (received_at) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS moderation_reports_server ON moderation_reports (server_id);

-- moderation_actions: each takedown and restore a service admin made, with its basis, and the
-- signed instruction's state on the home (pending until the home acknowledges it).
CREATE TABLE IF NOT EXISTS moderation_actions (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    kind            TEXT        NOT NULL CHECK (kind IN ('takedown', 'restore')),
    basis           TEXT        NOT NULL CHECK (basis IN ('notice', 'report', 'authority', 'admin_knowledge')),
    scope           TEXT        NOT NULL CHECK (scope IN ('item', 'source', 'channel', 'publisher')),
    notice_id       UUID        REFERENCES moderation_notices(id),
    report_id       UUID        REFERENCES moderation_reports(id),
    restores        UUID        REFERENCES moderation_actions(id),
    server_id       UUID        NOT NULL,
    channel_id      UUID,
    publisher_id    UUID,
    at_time         TIMESTAMPTZ,
    item_ref        TEXT        NOT NULL DEFAULT '',
    source_kind     TEXT        NOT NULL DEFAULT ''
        CHECK (source_kind IN ('', 'channel', 'playlist', 'speaker', 'label')),
    source_ref      TEXT        NOT NULL DEFAULT '',
    programme       TEXT        NOT NULL DEFAULT '',
    -- the kill switch's entry this action added (a programme's slate, a channel or a publisher)
    revocation_id   BIGINT      REFERENCES broadcast_revocations(id),
    home_state      TEXT        NOT NULL DEFAULT 'pending'
        CHECK (home_state IN ('pending', 'applied', 'not_found', 'failed', 'none')),
    home_detail     TEXT        NOT NULL DEFAULT '',
    home_at         TIMESTAMPTZ,
    reason          TEXT        NOT NULL,
    actor_id        TEXT        NOT NULL,
    actor_email     TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    restored_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS moderation_actions_pending ON moderation_actions (server_id) WHERE home_state = 'pending';
CREATE INDEX IF NOT EXISTS moderation_actions_server ON moderation_actions (server_id, created_at DESC);

-- moderation_strikes: a valid notice that led to a takedown is a strike on the publisher for
-- 12 months (broadcast.strikes.window_months); a withdrawn strike no longer counts.
CREATE TABLE IF NOT EXISTS moderation_strikes (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    publisher_id    UUID        NOT NULL,
    notice_id       UUID        NOT NULL REFERENCES moderation_notices(id),
    action_id       UUID        NOT NULL REFERENCES moderation_actions(id),
    at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    withdrawn_at    TIMESTAMPTZ,
    withdrawn_by    TEXT        NOT NULL DEFAULT '',
    withdraw_reason TEXT        NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS moderation_strikes_active ON moderation_strikes (publisher_id, expires_at) WHERE withdrawn_at IS NULL;

-- moderation_counter_notices: a publisher's § 512(g) counter-notice to a takedown. The item can be
-- restored 10 business days after it was filed unless the claimant says they went to court.
CREATE TABLE IF NOT EXISTS moderation_counter_notices (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    action_id     UUID        NOT NULL REFERENCES moderation_actions(id),
    notice_id     UUID        REFERENCES moderation_notices(id),
    server_id     UUID        NOT NULL,
    publisher_id  UUID        NOT NULL,
    name          TEXT        NOT NULL,
    address       TEXT        NOT NULL,
    phone         TEXT        NOT NULL DEFAULT '',
    email         TEXT        NOT NULL,
    explanation   TEXT        NOT NULL,
    signature     TEXT        NOT NULL,
    filed_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    restore_after TIMESTAMPTZ NOT NULL,
    lawsuit_at    TIMESTAMPTZ,
    lawsuit_by    TEXT        NOT NULL DEFAULT '',
    restored_at   TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS moderation_counter_notices_one ON moderation_counter_notices (action_id);

-- moderation_statements: what the publisher is told (DSA Art. 17 statements of reasons, strike
-- warnings, the end of broadcasting, a restore), shown on the home's Broadcast page and emailed.
CREATE TABLE IF NOT EXISTS moderation_statements (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    server_id    UUID        NOT NULL,
    publisher_id UUID        NOT NULL,
    action_id    UUID        REFERENCES moderation_actions(id),
    kind         TEXT        NOT NULL CHECK (kind IN ('takedown', 'strike_warning', 'terminated', 'restored', 'report')),
    params       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    emailed_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS moderation_statements_server ON moderation_statements (server_id, created_at DESC);

-- moderation_terminations: a publisher whose broadcasting ended under the repeat-infringer policy,
-- and a reinstatement (both by a service admin's hand or the third strike).
CREATE TABLE IF NOT EXISTS moderation_terminations (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    publisher_id     UUID        NOT NULL,
    revocation_id    BIGINT      REFERENCES broadcast_revocations(id),
    at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    reinstated_at    TIMESTAMPTZ,
    reinstated_by    TEXT        NOT NULL DEFAULT '',
    reinstate_reason TEXT        NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS moderation_terminations_live ON moderation_terminations (publisher_id) WHERE reinstated_at IS NULL;

-- moderation_notifier_blocks: notifiers suspended from the form after repeated manifestly
-- unfounded notices (DSA Art. 23), by email.
CREATE TABLE IF NOT EXISTS moderation_notifier_blocks (
    email      TEXT        PRIMARY KEY,
    reason     TEXT        NOT NULL,
    blocked_by TEXT        NOT NULL,
    blocked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- moderation_audit: every staff action on the queue (who, when, what, why). Append-only.
CREATE TABLE IF NOT EXISTS moderation_audit (
    id          BIGSERIAL   PRIMARY KEY,
    at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_id    TEXT        NOT NULL,
    actor_email TEXT        NOT NULL DEFAULT '',
    verb        TEXT        NOT NULL,
    target_kind TEXT        NOT NULL,
    target_id   TEXT        NOT NULL,
    reason      TEXT        NOT NULL DEFAULT '',
    detail      JSONB       NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS moderation_audit_at ON moderation_audit (at DESC);

-- +goose Down
DROP TABLE IF EXISTS moderation_audit;
DROP TABLE IF EXISTS moderation_notifier_blocks;
DROP TABLE IF EXISTS moderation_terminations;
DROP TABLE IF EXISTS moderation_statements;
DROP TABLE IF EXISTS moderation_counter_notices;
DROP TABLE IF EXISTS moderation_strikes;
DROP TABLE IF EXISTS moderation_actions;
DROP TABLE IF EXISTS moderation_reports;
DROP TABLE IF EXISTS moderation_notices;
ALTER TABLE broadcast_channels DROP COLUMN IF EXISTS edge_channel;
