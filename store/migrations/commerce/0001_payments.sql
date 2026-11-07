-- store-commerce (D1), story 54.19: paid plugins with pluggable payment providers.
--
-- One ledger in the store's own schema, whatever provider took a sale: each provider's
-- records are mapped into these tables by payments/ and commerce/, and nothing here names a
-- provider beyond its id. Additive only (story 54.15 §3): the previous Worker version keeps
-- working during a deploy.

-- the providers the store admin enabled, in order (payments/registry.ts); no secret here:
-- secrets are Worker secrets PAYMENTS_<ID>_<NAME>
CREATE TABLE IF NOT EXISTS payment_providers (
    id                  TEXT PRIMARY KEY,
    enabled             INTEGER NOT NULL DEFAULT 0,
    priority            INTEGER NOT NULL DEFAULT 100,
    mode                TEXT NOT NULL DEFAULT 'test' CHECK (mode IN ('test', 'live')),
    regions             TEXT NOT NULL DEFAULT '[]',
    currencies          TEXT NOT NULL DEFAULT '[]',
    resale_confirmed_at TEXT,
    settings            TEXT NOT NULL DEFAULT '{}',
    updated_by          TEXT NOT NULL DEFAULT '',
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- a developer's account with one provider (marketplace providers pay it themselves)
CREATE TABLE IF NOT EXISTS provider_accounts (
    developer_id TEXT NOT NULL,
    provider_id  TEXT NOT NULL,
    account_ref  TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('active', 'pending', 'restricted')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (developer_id, provider_id)
);

-- the one-time seller fee (a purchase of item seller_fee), once per developer
CREATE TABLE IF NOT EXISTS seller_fees (
    developer_id TEXT PRIMARY KEY,
    purchase_id  TEXT NOT NULL,
    paid_at      TEXT NOT NULL
);

-- a plugin's products and prices (story 54.19 §4)
CREATE TABLE IF NOT EXISTS products (
    plugin_id    TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('one_time', 'yearly')),
    developer_id TEXT NOT NULL,
    active       INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (plugin_id, kind)
);

CREATE TABLE IF NOT EXISTS prices (
    plugin_id    TEXT NOT NULL,
    kind         TEXT NOT NULL,
    currency     TEXT NOT NULL,
    amount_minor INTEGER NOT NULL CHECK (amount_minor > 0),
    PRIMARY KEY (plugin_id, kind, currency)
);

-- one sale: a plugin, or the seller fee (plugin_id NULL, developer_id the payer)
CREATE TABLE IF NOT EXISTS purchases (
    id              TEXT PRIMARY KEY,
    item            TEXT NOT NULL CHECK (item IN ('plugin', 'seller_fee')),
    account_id      TEXT NOT NULL,
    home_id         TEXT NOT NULL DEFAULT '',
    plugin_id       TEXT,
    product         TEXT,
    developer_id    TEXT NOT NULL,
    provider_id     TEXT NOT NULL,
    provider_ref    TEXT NOT NULL DEFAULT '',
    currency        TEXT NOT NULL,
    amount_minor    INTEGER NOT NULL,
    maktaba_minor   INTEGER NOT NULL,
    developer_minor INTEGER NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'paid', 'failed', 'refunded', 'disputed', 'lost')),
    period_end      TEXT,
    created_at      TEXT NOT NULL,
    paid_at         TEXT,
    CHECK (maktaba_minor + developer_minor = amount_minor)
);
CREATE INDEX IF NOT EXISTS purchases_home ON purchases (home_id, plugin_id);
CREATE INDEX IF NOT EXISTS purchases_developer ON purchases (developer_id, paid_at);

-- the ledger: every movement of money, signed, per party ('maktaba' or 'developer:<id>');
-- the sum per purchase and party is what that party holds of it
CREATE TABLE IF NOT EXISTS ledger_entries (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    purchase_id       TEXT,
    payout_id         TEXT,
    provider_id       TEXT NOT NULL,
    kind              TEXT NOT NULL
                      CHECK (kind IN ('sale', 'refund', 'dispute_lost', 'dispute_won', 'payout', 'payout_failed')),
    party             TEXT NOT NULL,
    amount_minor      INTEGER NOT NULL,
    currency          TEXT NOT NULL,
    provider_event_id TEXT,
    at                TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ledger_party ON ledger_entries (party, currency, at);

-- every provider event processed, once: a webhook delivered twice changes nothing
CREATE TABLE IF NOT EXISTS provider_events (
    provider_id TEXT NOT NULL,
    event_id    TEXT NOT NULL,
    type        TEXT NOT NULL,
    received_at TEXT NOT NULL,
    PRIMARY KEY (provider_id, event_id)
);

-- monthly payouts the store sends through a store_batches provider (story 54.19 §3)
CREATE TABLE IF NOT EXISTS payouts (
    id            TEXT PRIMARY KEY,
    developer_id  TEXT NOT NULL,
    provider_id   TEXT NOT NULL,
    period        TEXT NOT NULL,
    currency      TEXT NOT NULL,
    amount_minor  INTEGER NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('draft', 'approved', 'sent', 'paid', 'failed', 'rolled_over')),
    approved_by   TEXT,
    provider_ref  TEXT,
    created_at    TEXT NOT NULL,
    UNIQUE (developer_id, provider_id, period, currency)
);

-- the entitlements the store signed for a purchase (story 54.19 §6); the signature is
-- made again at each daily refresh, this row says until when and whether it ended
CREATE TABLE IF NOT EXISTS entitlements (
    purchase_id TEXT PRIMARY KEY,
    home_id     TEXT NOT NULL,
    plugin_id   TEXT NOT NULL,
    product     TEXT NOT NULL,
    period_end  TEXT,
    ended_at    TEXT,
    ended_why   TEXT
);
CREATE INDEX IF NOT EXISTS entitlements_home ON entitlements (home_id);
