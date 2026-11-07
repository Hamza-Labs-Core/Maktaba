-- store-commerce (D1), story 54.19 §5: a home's purchase intents.
--
-- A linked home asks for one with its own signature (POST /v1/homes/{id}/purchase-intents)
-- when its admin clicks Buy; the admin's browser posts it to /buy/<plugin>, where the buyer
-- signs in to the store account the home is linked to and is sent to the provider's
-- checkout. An intent is used once and lasts 10 minutes; only the sha256 of its token is
-- kept. Additive only (story 54.15 §3).
CREATE TABLE IF NOT EXISTS purchase_intents (
    id_hash     TEXT PRIMARY KEY,
    home_id     TEXT NOT NULL,
    plugin_id   TEXT NOT NULL,
    product     TEXT NOT NULL CHECK (product IN ('one_time', 'yearly')),
    lang        TEXT NOT NULL DEFAULT 'en' CHECK (lang IN ('en', 'ar')),
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    used_at     TEXT,
    purchase_id TEXT
);
CREATE INDEX IF NOT EXISTS purchase_intents_home ON purchase_intents (home_id, created_at);
