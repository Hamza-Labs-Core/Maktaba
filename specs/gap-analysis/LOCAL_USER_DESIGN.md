# Design — Offline-first local user, server-authoritative on connect

> Decision inputs (from product owner):
> **Model = offline-first local account** · **Identity authority =
> server wins on connect.**
>
> This design is deliberately reconciled against the *actual* code
> state surfaced by the behavioral gap analysis (`MASTER.md`), not the
> aspirational specs. It both delivers the feature and folds in three
> existing gaps it naturally closes.

## Goal

A user can install Maktaba, create an account, browse a local library
and watch media **with no server**. Later they point the app at a
self-hosted Maktaba server, authenticate, and their device + local
user-state is **linked into the server account, which is canonical**.

## Model

**Local provisional identity** (on-device SQLite — the existing
`*.sqlite.sql` parity DB):

- `local_uid` UUIDv7, `username`, argon2id hash (reuse
  `api/internal/auth/users` pointed at the SQLite DSN — no new crypto).
- **Device keypair**: Ed25519 generated on first run, private key in
  OS keychain (desktop/mobile) or `0600` file (headless). The public
  key is the durable device identity. *This is the same primitive the
  missing Story 10.18 "Ed25519 server identity" needs — build once,
  use both places.*
- All user-content rows carry `origin`, `dirty`, `updated_at` for
  later reconciliation.

## Offline operation (no behavioral fork)

Run the existing Go binary in `--local` mode (SQLite DSN). The SPA is
unchanged: it still does cookie/JWT auth and the `GET /api/auth/me`
probe. Difference: tokens are RS256-signed by a **device-local key**
(`kid=local:<device_id>`, `iss=local`). Login validates against
`local_user` via the existing `users` package. Playback of
already-present media + sidecars needs no pipeline; scan/transcode are
deferred or run locally once root-cause **R1** is fixed.

## Connect flow — `local_only → linking → linked`

1. User enters a server URL and authenticates with **server**
   credentials via the existing `POST /api/auth/login` (a server
   account must exist — `adduser` or admin API).
2. Client calls **new `POST /api/auth/link`** (auth: server bearer)
   with `{ device_pub, local_uid, manifest_digest }`. Server:
   - binds `device_pub` to the authenticated server user
     (`user_devices` row — reuse the `pairing_tickets` table shape);
   - records `local_link(server_uid, local_uid, device_id, server_url)`;
   - returns `{ server_uid, device_refresh_token (30d), expires_at }`.
   *This is exactly the device refresh token that epic-15's
   `POST /api/pairing/exchange` is supposed to mint but currently does
   not — implement once in this handler and route pairing through it.*
3. `linking`: client replays local-only mutations (watch progress,
   collections, tags, settings — **not** local file paths; the server
   owns its own roots) to **`POST /api/auth/link/replay`**, each op
   keyed by a client `op_id` through the existing Epic-24
   idempotency-key middleware (verify it is actually wired — epic-24
   reports it orphaned).
4. On ack: rewrite local rows `owner: local_uid → server_uid`, set
   `link_state=linked`, retain `local_link` for audit. Future logins
   prefer the server; offline falls back to the cached device-signed
   JWT until the refresh token can be exchanged.

## Conflict policy (server is authority)

| Data class | Rule |
|---|---|
| Identity: username, `is_admin`, ACL/`lib[]` | **Server always wins.** Local provisional `is_admin=true` is dropped unless the server account is admin. |
| User content: progress, collections, tags, settings | Last-write-wins by `updated_at`; **server wins ties**. |
| Deletions | Additive merge only; a delete requires an explicit tombstone with a strictly newer `updated_at`. Replay never hard-deletes server rows. |
| Re-link device to a *different* server user | Refuse (1 device : 1 server identity) unless `--force-relink`, which unlinks first and writes an audit-log entry. |

## Schema

On-device (SQLite only):
```
local_user(local_uid PK, username, pw_hash, device_id, created_at,
           link_state CHECK IN ('local_only','linking','linked'),
           linked_server_uid NULL, linked_at NULL)
local_link(local_uid, server_uid, server_url, device_id, linked_at,
           PRIMARY KEY(local_uid, server_url))
sync_op(op_id PK, kind, payload_json, created_at, acked_at NULL)
```
Server (new Postgres migration slot + SQLite parity):
```
user_devices(server_uid, device_id, device_pub, created_at, last_seen,
             PRIMARY KEY(server_uid, device_id))
```

## Server endpoints

```
GET  /api/auth/me                 # R3 prerequisite — also required here
POST /api/auth/link               # server bearer; -> server_uid + device refresh token
POST /api/auth/link/replay        # device token; idempotent (op_id); additive merge
```

## Hard dependencies (must land first)

1. **R3** (`MASTER.md`): install the `RequireAuth` gate, mount
   `GET /api/auth/me`, stop discarding `CookieAuth`, populate JWT
   `lib[]`. Without R3 "server is authority" has nothing to
   authenticate against and the linked session cannot restore.
2. `--local` server mode + device-local JWT `kid`/issuer (new, small).
3. Epic-24 idempotency-key middleware actually wired (gap analysis
   flags it orphaned — verify before relying on it for replay).

## Phased plan

| Phase | Deliverable | Closes |
|---|---|---|
| P0 | R3 fix (auth gate + `/api/auth/me`) | auth bypass + web session-restore |
| P1 | `--local` mode, device-signed JWT, `local_user` on SQLite, offline login + playback | the feature's offline half |
| P2 | `POST /api/auth/link` + `user_devices` + device refresh token | also fixes epic-15 broken `pairing/exchange` |
| P3 | `sync_op` queue + `/link/replay` idempotent additive merge + conflict policy | offline→server data migration |
| P4 | re-link guard, tombstones, audit entries | needs epic-21 append-only `audit_log` fix to be trustworthy |

## Why this shape

- **Zero auth fork**: local mode reuses the same SPA auth code and the
  same `users`/RS256 machinery — only the signing key/issuer differ.
- **One device-identity primitive** serves local login, server
  linking, pairing (epic-15), and the missing Ed25519 server identity
  (Story 10.18).
- **Server-authority** keeps the conflict model tractable: identity is
  never merged, only user-content is, with a deterministic tiebreak.
- It is honest about ordering: nothing here works until **R3** lands,
  because every "connect to the server" path presumes an auth gate
  that does not currently exist.
