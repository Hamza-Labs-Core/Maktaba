# Plan 35.16 — The cluster link: machine-to-machine TLS — implementation

> Implementation plan for [story-35-16-cluster-link-tls](story-35-16-cluster-link-tls.md). Design: [design.md](design.md) §18, §11.5.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | WebSockets over mutual TLS on the API's existing port, TLS sniffed on the first byte. | Owner's choice; reuses the Epic 31 hub and libraries; no new port or firewall rule. |
| D2 | One control channel per machine, authenticated first; every data stream on its own `wss://` with a single-use ticket. | Owner: "auth first, then stream on another"; no head-of-line blocking between streams; each stream is authorised by a live control session. |
| D3 | Per-frame sealing dropped on the link; kept on the legacy WebSocket and end to end for forwarded pipeline sessions. | TLS 1.3 + per-machine certificates give the same guarantees; double crypto and strict counters would only slow the mux. |
| D4 | Nodes always dial; the home never connects to a node (`stream.offer` for home-initiated pulls). | One direction through firewalls; nodes listen on loopback only. |
| D5 | Streams survive a control reconnect for 60 s, then close; revocation closes everything at once. | A home API restart must not cut TV; a revoked machine must lose access immediately. |
| D6 | Postgres stays its own TCP connection with `verify-full` + client certificate (CN = role). | Keeps drivers' pools, reconnect and LISTEN; tunnelling rejected (design §18.8). |
| D7 | CA key on the home only, file + sealed copy in `home_secrets`. | Survives a restore; never on nodes. |
| D8 | `prefer` by default, automatic `require` after 24 h of every machine on the link. | Moves existing machines without a flag day; closes the legacy paths afterwards. |

## 1. Files

**Create**
- `api/internal/cluster/ca/` — `ca.go` (create, load, sign CSR, rotate), `store.go` (`cluster_certs`), tests.
- `api/internal/cluster/link/` — `listener.go` (TLS sniffing `net.Listener` splitting TLS and plain
  HTTP), `control.go` (control channel server: auth, hello, framing, class scheduler), `tickets.go`
  (issue, verify, single use, alert), `streams.go` (stream endpoint, re-attach, grace, limits),
  `frames.go` (wire format shared with the client), tests.
- `cmd/maktaba-server/internal/link/` — node side: `client.go` (dial, backoff, pinned CA), `roundtrip.go`
  (HTTP `RoundTripper` over the control channel), `streams.go` (open on ticket, serve `stream.offer`),
  `local.go` (loopback agent endpoint for pipeline processes, forwarding their v2 sessions), tests.
- `cmd/maktaba-server/cmd_setup_pgtls.go` — `setup --pg-tls`.
- `shared/db/migrations/<S4>_cluster_certs.sql` (+ `.sqlite.sql`), slot next free at implementation time.
- `docs/cluster/tls.md` — the link, the CA, installing the CA for browser HTTPS per OS.

**Modify**
- `api/internal/handlers/cluster/` join/approve — `csr` in the request, `tls` in the sealed bundle.
- `api/internal/cluster/agent/hub.go` — `cert.csr`, `cert.renew`; sessions arriving through the link.
- `pipeline/src/maktaba_pipeline/cluster/` — join sends a CSR (key via `cryptography`); the agent
  dials the local supervisor endpoint when present.
- `streaming/internal/frontdoor` — upstream for node-hosted channels via `tv_pull`/`relay_pull`
  streams; `streaming/internal/cluster` — node serves `stream.offer`, binds loopback in `require`.
- `api/internal/cluster/homeproxy` (35.10), delegation (35.9), mirror (35.12) — transport selection by
  `cluster.link.mode`.
- `cmd/maktaba-server/internal/supervisor/env.go` — Postgres DSN with `sslmode=verify-full` and cert
  paths when the node has a certificate; `config/nodeconfig.go` — `tls` block.
- `web/src/pages/admin/Machines*` — link state, certificate expiry, mode, CA fingerprint, VOD-fallback badge.
- `api/internal/handlers/rules/schema.json` — `cluster.link.*`, `cluster.tls.*`.

## 2. Steps

1. CA and `cluster_certs`; issue the home's `server` certificate; TLS sniffer on the API port.
2. Join with CSR; `cert.csr` over v2 for joined machines; renewal.
3. Control channel (auth, hello, framing, scheduler); the node client and the `RoundTripper`; move
   session checks, `home` proxy, service door, update commands onto it.
4. Tickets and data streams; artifacts, wheelhouse, backups, `ws_proxy`; then `tv_pull` / `relay_pull`
   in the front door; node streaming port to loopback in `require`.
5. Reconnect, grace and re-attach; revocation closing everything.
6. Postgres TLS (`setup --pg-tls`, DSN, `pg_hba` in `require`).
7. Mode switch (`prefer` → automatic `require`), legacy refusal, Machines page.
8. Optional web HTTPS and docs.

## 3. Tests

The story's TCs; plus a fuzz test of the control framing and ticket parsing, and a chaos test that
drops the control channel every 20 s for 10 minutes with 5 TV pulls open (no viewer interruption).

## 4. Rollout

Phase C with the full nodes. Ships with `prefer`; the existing joined machine (this PC) moves on its
first update without re-joining; `require` turns on by itself once every machine has been on the link
for a day.
