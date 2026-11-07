-- store-commerce (D1), story 54.19 §6: moving a purchase to another home.
--
-- The buyer's store account moves a plugin purchase to another of its linked homes, at most
-- twice in 365 days per purchase, after re-authentication (POST /v1/purchases/{id}/move).
-- The move rewrites purchases.home_id and entitlements.home_id; the old home loses the
-- entitlement at its next daily refresh, because a home replaces its plugin_entitlements
-- with what the store returns. One row per move, kept for the count and for support.
-- Additive only (story 54.15 §3).
CREATE TABLE IF NOT EXISTS purchase_moves (
    id          TEXT PRIMARY KEY,
    purchase_id TEXT NOT NULL,
    account_id  TEXT NOT NULL,
    from_home   TEXT NOT NULL,
    to_home     TEXT NOT NULL,
    at          TEXT NOT NULL,
    CHECK (from_home <> to_home)
);
CREATE INDEX IF NOT EXISTS purchase_moves_purchase ON purchase_moves (purchase_id, at);
