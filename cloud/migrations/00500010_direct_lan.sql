-- +goose Up
-- Story 50.15 §3 (DNS rebinding): the home's direct listener's LAN addresses (ip:port) from its
-- META_ENDPOINTS report, so the home list carries its LAN name and the raw-IP entry, pinned to
-- that name, for a router that blocks names resolving to LAN addresses.
ALTER TABLE servers ADD COLUMN IF NOT EXISTS direct_lan TEXT[];

-- +goose Down
ALTER TABLE servers DROP COLUMN IF EXISTS direct_lan;
