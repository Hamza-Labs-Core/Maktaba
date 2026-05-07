# Story 25.1 — Cloud API service bootstrap

> Epic 25 · Cloud relay · Phase 1 (foundation)

## Description

Stand up the `maktaba-cloud` Go service, its Postgres database, and the
Cloudflare-fronted edge so every later story has a place to land. This
is the equivalent of [Epic 07 Story 07.1](../07-api-server/README.md)
but for the *cloud-side* binary. By the end of this story:

- `maktaba-cloud serve --role=api` listens on `:8080` and answers
  `GET /healthz` (200 `ok`) and `GET /readyz` (200 if DB reachable).
- A managed Postgres on Hetzner (`maktaba-cloud-prod`) holds the
  cloud schema in its own DB (separate from any local server DB).
- Migrations run via `goose` against `cloud/migrations/`, claiming
  slots `0001`–`0010` for Epic 25 (see README).
- Cloudflare DNS points `cloud.maktaba.app` (the bootstrapping admin
  hostname) and `api.maktaba.app` (the public API host) at the
  Hetzner LB; TLS is terminated at Cloudflare for these two hosts.
- The binary takes a single `--config /etc/maktaba/cloud.toml`
  pointing at `[database]`, `[oauth]`, `[stripe]`, `[apns]`, `[fcm]`,
  `[admin]` sections — secrets read from environment overrides
  (`MAKTABA_CLOUD_DB_URL`, etc.) for 12-factor compliance.
- Structured `slog` JSON logs include `request_id`, `user_id`,
  `server_id` correlation fields. A `request_id` is minted at the
  edge if absent (`X-Request-Id` is propagated downstream).
- Prometheus `/metrics` exposes `http_requests_total{route, code}`,
  `http_request_duration_seconds`, `db_pool_in_use`, plus per-role
  gauges (`tunnels_open`, `streams_in_flight` from later stories).

This story does **not** include any business endpoints — those land in
25.2 onwards. It exists so that a developer running `make cloud-dev`
sees a process, a DB, and a 200 OK.

## Acceptance criteria

- **Given** an empty Postgres and a fresh checkout of `cloud/`,
  **when** the operator runs `goose -dir cloud/migrations up`,
  **then** schema slots `0001`–`0010` apply in order with no errors.
- **Given** the binary started with `--role=api`,
  **when** a client requests `GET /healthz`,
  **then** the response is `200 OK` with body `{"status":"ok","version":"0.1.0","commit":"<sha>"}`.
- **Given** Postgres is unreachable,
  **when** a client requests `GET /readyz`,
  **then** the response is `503 Service Unavailable` and the
  `db_up{db="cloud"}` gauge is `0`.
- **Given** the binary is started without `--role`,
  **when** the process initializes,
  **then** it errors with exit code `2` and message
  `--role required: api | relay | worker`.
- **Given** an HTTP request with no `X-Request-Id`,
  **when** the request enters the API,
  **then** the response includes a freshly-minted `X-Request-Id`
  header (UUIDv7, 36 chars) and the same id appears in every log
  line for that request.
- **Given** `MAKTABA_CLOUD_DB_URL` is set,
  **when** the binary starts,
  **then** the env value overrides `[database].url` from the TOML.
- **Given** the binary built with `-ldflags '-X main.commit=...'`,
  **when** the operator runs `maktaba-cloud version`,
  **then** stdout is the commit SHA, build time, and Go version.

## Test cases

| ID    | Type        | Setup | Action | Expected |
|-------|-------------|-------|--------|----------|
| T01   | unit        | router fixture | request `/healthz` | 200, JSON shape valid |
| T02   | unit        | DB pool with closed connection | request `/readyz` | 503 |
| T03   | integration | docker-compose Postgres | `goose up` then `goose down 1` | reversible without data loss |
| T04   | integration | binary with `MAKTABA_CLOUD_DB_URL=invalid://` | start | exits 1 within 5s, log line includes "db unreachable" |
| T05   | integration | start with `--role=api` | scrape `/metrics` | exposes `http_requests_total`, `go_*` |
| T06   | integration | concurrent requests, 100 RPS for 30s | inspect log lines | every line has matching `request_id` for its request |
| T07   | smoke       | deploy to staging | curl `https://api.maktaba.app/healthz` | 200, valid TLS chain |
| T08   | smoke       | deploy to staging | curl `-H 'Host: cloud.maktaba.app' https://<lb-ip>/healthz` | 200 (admin host responds) |
| T09   | unit        | TOML missing `[database]` | start | exits 2, "missing required section: database" |
| T10   | regression  | run `--role=worker` (no API endpoints) | curl `/healthz` | 404 (no HTTP listener for worker role) |

## Edge cases

- **Concurrent migrations.** Two operators running `goose up`
  simultaneously: goose's advisory lock (Postgres `pg_advisory_lock`)
  serializes them. Document the lock id (`8472612`) in the migration
  README so two cloud installs in the same DB cluster don't collide.
- **DB connection storm on cold start.** Pool maxes at 25 connections
  per pod; with 4 pods we cap at 100 conns vs. Hetzner managed PG's
  200 limit. Leaves headroom for `psql` operator sessions.
- **Time-zone drift between pods.** All `time.Now()` calls go through
  a `clock.Now()` indirection so tests can freeze; production always
  uses UTC; `TZ=UTC` set in container env.
- **Log volume.** A single `slog.Info` per request at 100 RPS = ~8M
  log lines/day. Sample to 10% on `2xx` after one week of operation;
  always full-fidelity on `4xx`/`5xx`.
- **Restart during migration.** Goose marks each migration applied
  inside the migration transaction, so a SIGTERM mid-migration
  rolls back cleanly; the next start retries.
- **Secrets in flight.** TOML may contain placeholders only; real
  values come from env. CI lints any `[secret]`-shaped key in TOML
  and fails the build if it's a non-placeholder.
- **IPv6 only Hetzner LB.** Cloudflare proxies handle the v4↔v6
  bridge; document this for any operator who tries to bypass
  Cloudflare and curl the LB directly.
- **Multi-region.** Out of scope for v1 — single region (`fsn1`,
  Falkenstein). The schema does not encode region; a future v2
  multi-region rollout is allowed to add it.

## Files / packages

- `cloud/cmd/maktaba-cloud/main.go` — entry point.
- `cloud/internal/server/router.go` — chi router; mounts `/healthz`,
  `/readyz`, `/metrics`.
- `cloud/internal/server/middleware/{logging,recover,requestid}.go`.
- `cloud/internal/db/pool.go` — pgx pool with health probe.
- `cloud/migrations/00010001_init_users_sessions.sql` — slot 0001.
- `cloud/configs/cloud.example.toml` — annotated template.

## Open questions

- **Do we run on Fly.io instead of Hetzner?** Fly's egress is more
  expensive but multi-region is one-click. Decision deferred to 25.7
  (relay) since that's where region matters; v1 starts on Hetzner.
- **Postgres extensions.** We need `citext` (emails, subdomains) and
  `pgcrypto` (UUIDv7 generation). Hetzner managed PG enables both
  by default; verify before promoting to prod.
