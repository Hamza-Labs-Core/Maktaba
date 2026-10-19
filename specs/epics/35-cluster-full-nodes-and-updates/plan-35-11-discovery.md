# Plan 35.11 — Discovery of every machine — implementation

> Implementation plan for [story-35-11-discovery](story-35-11-discovery.md). Design: [design.md](design.md) §12.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | The supervisor is the single advertiser on every machine; the API's advertiser runs only when the API runs without a supervisor (`serve --role api` in containers). | One publication per machine; the supervisor survives role restarts during an update. |
| D2 | Same service type `_maktaba._tcp`, new TXT keys, `protocol` stays `1`. | Additive: old browsers keep working, old apps list nodes as servers (a full node is a full Maktaba). |
| D3 | Group by `home` id, remember by home instance id. | Addresses change (DHCP); the instance id does not. |
| D4 | `join` keeps ignoring nodes. | One cannot join a node; the pairing endpoints exist only on the home. |

## 1. Files

**Modify**
- `cmd/maktaba-server/internal/mdns/mdns.go` — `Advertise(opts)` with role, ids, web port, name,
  version; `Republish`; tests.
- `cmd/maktaba-server/cmd_serve.go` — pass node-mode fields from `config.LocalNodeConfig()`; republish
  after an update (the new supervisor starts with the new version anyway; port changes on config
  reload).
- `api/internal/discovery/mdns.go` — skip when `MAKTABA_LAUNCHER=1` or `MAKTABA_NODE_MODE=1`
  (supervisor advertises); share the TXT builder.
- `api/internal/cluster/advertise.go` — expose the TXT builder (`role`, `home`, `id`, `join`).
- `api/internal/system/version.go` — `role`, `home_instance_id`, `node_id`, `web_url`.
- `pipeline/src/maktaba_pipeline/cluster/discover.py` — filter `role=node`; probe fallback filters
  `role: node`.
- `apps/desktop/src-tauri/src/lib.rs` — `discover_servers` returns TXT `role`, `home`, `id`, `web`,
  `name`; grouping in the front end.
- `apps/mobile/src/…` server picker (and the shared picker module used by desktop/TV) — grouping,
  preference, remembered cluster.
- `web/src/pages/Admin/Machines.tsx` — machine name links to `web_url`.
- `docs/cluster/README.md` — "Open Maktaba from any machine".

**Create**
- `api/internal/router` route `GET /api/cluster/self` (local class): this machine plus the cluster's
  known machine addresses (from the home when reachable, cached otherwise).

## 2. Steps

1. TXT builder shared by both advertisers; unit tests for home and node.
2. Supervisor advertiser in node mode; API advertiser suppression; republish hook.
3. Version endpoint fields; `/api/cluster/self`.
4. Python join filter.
5. Desktop discovery fields; picker grouping and fallback (desktop, mobile, TV).
6. Machines page link; docs.

## 3. Tests

Story TC1–TC7; plus a manual check on a LAN with `dns-sd -B _maktaba._tcp` (macOS) and
`avahi-browse -rt _maktaba._tcp` (Linux) showing one entry per machine.

## 4. Rollout

Ships with phase C (35.8). Old apps are unaffected (they list nodes as servers); new apps against an
old home see no `role` and treat the entry as a home.
