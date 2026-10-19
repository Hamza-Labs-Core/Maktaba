# Plan 32.6 — Cloudflare Stream integration — implementation

> Implementation plan for [story-32-06](story-32-06-cloudflare-integration.md).
> Writes slot **0120** (`home_secrets`; 0119 went to Epic 31's `cluster_cache` first). Design: [design.md](design.md) §7.1, §7.6.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Secrets in `home_secrets`, AES-256-GCM, key file `secrets.key` (0600) in the home's config dir. | Not readable by node roles (REVOKE) and not usable from a database dump alone. |
| D2 | The integration is listed with the extensions (name `cloudflare_stream`) but its secret fields are `home_only`: stored in `home_secrets`, never in `extensions.settings`. | Same admin UX as Plex; fixes the class of leak (extension secrets readable by nodes). Every extension secret (the Plex token too) moves to `home_secrets` in this plan (README Q7, accepted default, revisit if needed). |
| D3 | All Cloudflare calls are made by the home's API (Go). | The token never leaves the home; nodes only ever get one push's ingest key. |
| D4 | The signing key is created by "Test connection" when missing and kept as `cf_signing_key` (id + PEM). | Local RS256 signing, no per-viewer API calls. |
| D5 | The spike runs before 32.7 merges and its findings are written into design.md and the runbook. | Four behaviours are not documented (README). |

## 1. Files

**Create**
- `shared/db/migrations/0120_home_secrets.sql`, `.sqlite.sql`; MANIFEST row; `pipeline/tests/db/test_home_secrets_migration.py`
- `api/internal/secrets/secrets.go` — `Store{DB, Key}`: `Put(name, []byte)`, `Get(name)`, `Delete(name)`, `Masked(name) bool`; key file load/create
- `api/internal/tvremote/cf/client.go` — HTTP client for `api.cloudflare.com/client/v4` (token auth, retries on 429/5xx with backoff, error decoding of `errors[]`)
- `api/internal/tvremote/cf/{inputs,videos,keys,analytics}.go` — the calls listed in design §7.1
- `api/internal/tvremote/integration.go` — save credentials, test connection, remove
- `api/internal/handlers/tvremote/admin.go` — `POST /api/tv/remote/test`, `GET /api/tv/remote` (integration part)
- `pipeline/src/maktaba_pipeline/extensions/cloudflare_stream/__init__.py` — manifest only (no stages): fields `account_id`, `api_token` (`secret=True, home_only=True`)
- tests (fake Cloudflare `httptest` server shared by 32.6–32.10: `api/internal/tvremote/cftest`)

**Modify**
- `pipeline/src/maktaba_pipeline/extensions/base.py` — `SettingField.home_only`
- `api/internal/handlers/queues/queues.go` (`PutExtension`, `Extensions`) — route `home_only` and `secret` fields to `secrets.Store`; return them masked
- `api/internal/secrets/migrate.go` — one-time, idempotent move of existing `secret` values (the Plex token) from `extensions.settings` to `home_secrets` at the home API's start; inventory of the readers of those values (pipeline Plex stages) so each reads through the home
- `web/src/pages/Admin/Extensions.tsx` — "Test connection" button for `cloudflare_stream`, result list
- `cmd/maktaba-server/internal/config` — the secrets key path (`<config dir>/secrets.key`)

## 2. Migration 0120

```sql
CREATE TABLE IF NOT EXISTS home_secrets (
  name        TEXT PRIMARY KEY,
  ciphertext  BYTEA NOT NULL,
  nonce       BYTEA NOT NULL,
  key_id      TEXT NOT NULL,          -- sha256(key)[:8], to detect a replaced key file
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by  UUID
);
-- REVOKE ALL ON home_secrets FROM maktaba_worker (guarded like 0103: only when the role exists)
```

## 3. Test connection

1. Token verify (`GET /user/tokens/verify`, or `GET /accounts/{id}/tokens/verify` for an
   account-owned token) → active.
2. `GET /accounts/{id}/stream/live_inputs` → Stream available (403/`ERR_MISSING_SUBSCRIPTION` → "no Stream subscription").
3. `GET /accounts/{id}/stream/storage-usage` → used / capacity minutes (shown; warn if capacity < `storage_plan_minutes`).
4. GraphQL probe `streamMinutesViewedAdaptiveGroups(limit:1)` → analytics ok / missing permission.
5. Signing key: if `cf_signing_key` absent → `POST /accounts/{id}/stream/keys`, store `{id, pem}`.
6. Customer domain: from the `playback.hls` of any input (`customer-<code>.cloudflarestream.com`),
   creating the first pool input disabled if none exists (it is reused by 32.7).
7. The home's push relay: its `can_push[tv_remote.ingest]` (Epic 33.8: only the home pushes).

Result `{ok, checked_at, lines:[{key, ok, detail}]}` stored in `app_settings` (`tv_remote.last_test`).

## 4. Spike (manual, real account)

Script `tools/tv/cf-spike.md` + a small Go command `tools/tv/cfspike` that: creates an input, pushes
the sample channel TS with ffmpeg, plays with the token for the video id and for the input id,
measures latency (burned-in clock vs wall clock), forces a 10 s and a 60 s ingest drop, stops,
deletes the recording, reads storage usage before/after, and creates N inputs to find the account
limit. Findings → design.md §7 and the runbook.

## 5. Tests

Secrets round trip, wrong key, worker REVOKE (Postgres), masked API output, each test-connection
line against the fake server, signing key created once, remove-integration flow.

## 6. Rollout

The integration page appears for admins; nothing else changes until `tv_remote.enabled`.
