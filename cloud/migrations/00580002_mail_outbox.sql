-- +goose Up
-- The account mails waiting to be sent, and those of the last weeks: a mail survives a restart,
-- and the limits (per address, per network, per day, per month) are counted here. What a row says
-- is sealed (AES-GCM under a key derived from the token secret, bound to the row's id) and cleared
-- once the mail is sent or given up; the address is kept as a keyed hash only, and the network that
-- asked as one for a day.
--
--   queued   a request ("a reset for this address"): whether there is an account is found out when
--            it is worked on, never while the person waits for an answer
--   ready    a mail waiting for its turn; its link is made and its words written when it has it
--   sending  being handed to the provider, until lease_until
--   sent | logged (the log driver: not delivered) | failed | expired | suppressed (this
--            deployment does not mail the recipient) | dropped (no account wants it, or one was
--            sent a moment ago: the address is gone, the row goes after a day)
CREATE TABLE IF NOT EXISTS mail_outbox (
    id          UUID        PRIMARY KEY,
    kind        TEXT        NOT NULL,
    -- stranger, repeat, account or notice (outbox.go); '' until the request is worked on
    class       TEXT        NOT NULL DEFAULT '',
    to_hash     TEXT        NOT NULL,
    net_hash    TEXT        NOT NULL DEFAULT '',
    -- the request passed the bot check
    checked     BOOLEAN     NOT NULL DEFAULT FALSE,
    lang        TEXT        NOT NULL DEFAULT '',
    state       TEXT        NOT NULL,
    sealed      TEXT,
    attempts    INTEGER     NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    next_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until TIMESTAMPTZ,
    expires_at  TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    -- the last refusal, as the provider's status and short error name
    last_error  TEXT        NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_mail_outbox_open ON mail_outbox (state, next_at) WHERE state IN ('queued', 'ready', 'sending');
CREATE INDEX IF NOT EXISTS idx_mail_outbox_to ON mail_outbox (to_hash, kind, created_at);
CREATE INDEX IF NOT EXISTS idx_mail_outbox_net ON mail_outbox (net_hash, created_at) WHERE net_hash <> '';
CREATE INDEX IF NOT EXISTS idx_mail_outbox_finished ON mail_outbox (finished_at) WHERE finished_at IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS mail_outbox;
