# Implementation Plan — Story 25.1 Cloud API service bootstrap

> Companion to [story-25-01-cloud-api-bootstrap.md](story-25-01-cloud-api-bootstrap.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Repository layout | New top-level `cloud/` tree, parallel to the local `api/` tree. Same Go module style. Separate `go.mod` rooted at `cloud/`. |
| Binary | Single `cmd/maktaba-cloud` with `--role api|relay|worker`. Cobra command tree; `serve`, `migrate`, `version`. |
| Database | A *separate* Postgres instance from any local server DB. Migrations live in `cloud/migrations/`, goose-managed, slots `0001`–`0010` claimed for Epic 25. |
| Router | `go-chi/chi/v5` (matches local API). Three router trees: `apiRouter`, `relayRouter`, `workerRouter` (worker has no HTTP at all, just `/healthz` on a control port). |
| Config | `cloud/configs/cloud.example.toml` template + env override via `caarlos0/env`-style binder. Secrets ONLY from env. |
| Logging | `log/slog` JSON handler with `request_id`, `user_id`, `server_id` fields. Middleware mints `X-Request-Id` (UUIDv7) at edge. |
| Metrics | `prometheus/client_golang`; `/metrics` exposed on the API role only (relay + worker scrape via sidecar). |
| Out of scope | Any business endpoint (auth, billing, relay). Those land 25.2+. |

## 1. Directory layout

```
cloud/
  cmd/maktaba-cloud/
    main.go              # entry point; wires Cobra + roles
    role_api.go          # role=api wiring
    role_relay.go        # stub (lands 25.8)
    role_worker.go       # stub (lands 25.14, 25.18, 25.19)
    version.go           # ldflags-injected commit/build/go
  internal/
    config/
      config.go          # TOML + env binder
      validate.go        # required-section check
    server/
      router.go          # chi router; /healthz, /readyz, /metrics
      health.go          # health & readiness probes
      shutdown.go        # graceful drain on SIGTERM
    middleware/
      requestid.go       # mint UUIDv7 if absent; propagate
      logging.go         # slog access log w/ correlation
      recover.go         # panic → 500 + log + metric
      cors.go            # CORS preflight (locked-down origin list)
    db/
      pool.go            # pgxpool init; health probe
      migrator.go        # goose wrapper
    obs/
      metrics.go         # prom registry + standard metrics
      tracer.go          # otel stub (no exporter v1)
    clock/
      clock.go           # injectable Now() for tests
  migrations/
    00010001_users_sessions.sql        # placeholder; lands 25.2
    ...0010 reserved per README.
  configs/
    cloud.example.toml
  go.mod / go.sum
```

The migration filename convention is `00<slot><seq>_<topic>.sql`; the leading 4 digits = slot, next 4 = within-slot ordering. Slot 0001 reserved here; real DDL lands in 25.2.

## 2. Config shape (`cloud.example.toml`)

```toml
[server]
listen_addr = "0.0.0.0:8080"
public_url  = "https://api.maktaba.app"
read_timeout  = "30s"
write_timeout = "60s"
shutdown_grace = "20s"

[database]
url = "postgres://maktaba_cloud@localhost:5432/maktaba_cloud?sslmode=require"
# Override: MAKTABA_CLOUD_DB_URL
max_open_conns = 25
max_idle_conns = 5
conn_max_lifetime = "30m"

[oauth.google]     # placeholders; wired in 25.3
client_id = ""
client_secret = ""  # env MAKTABA_CLOUD_OAUTH_GOOGLE_SECRET

[oauth.apple]
team_id = ""
key_id  = ""
client_id = ""
key_path  = ""

[stripe]
secret_key      = ""   # env MAKTABA_CLOUD_STRIPE_SECRET
webhook_secret  = ""   # env MAKTABA_CLOUD_STRIPE_WEBHOOK_SECRET

[apns]
team_id  = ""
key_id   = ""
key_path = ""

[fcm]
project_id = ""
service_account_path = ""

[admin]
allowed_email_domain = "hamzalabs.com"

[telemetry]
log_format = "json"
log_level  = "info"
sample_2xx_after_days = 7
sample_2xx_rate = 0.10
```

Validator enforces presence of `[server]`, `[database]`, and either `[oauth.google]` or `[oauth.apple]` blocks on `api` role; `relay` role waives oauth/stripe checks; `worker` waives `[server.listen_addr]`.

## 3. Endpoints (this story only)

| Method | Path | Roles | Purpose |
|---|---|---|---|
| GET | `/healthz` | api, relay | Liveness; 200 always while process responsive. |
| GET | `/readyz` | api, relay | Readiness; 200 iff DB pool reachable AND migrations applied to head. |
| GET | `/metrics` | api, relay | Prom scrape. |
| GET | `/.well-known/maktaba-cloud-version` | api | `{version, commit, built_at}`. |

`worker` role binds `127.0.0.1:9090` for the same endpoints (control port; not LB-exposed).

`/healthz` body:

```json
{"status":"ok","version":"0.1.0","commit":"<sha>","role":"api","built_at":"..."}
```

`/readyz` checks:

1. `pool.Ping(ctx, 2*time.Second)`.
2. `goose.Status` → most recent applied migration is at HEAD (or migrations are no-op for non-api roles? api always checks).
3. Each check exposes a gauge `db_up{db="cloud"}`, `migrations_at_head`.

## 4. Middleware order (api router)

```
recover → requestid → logging → metrics → cors → routes
```

- `requestid`: read `X-Request-Id`; if missing or malformed (must be UUIDv7-shaped, 36 chars, hex+hyphens), mint a new one. Set on `r.Context` via typed key; mirror to response header.
- `logging`: structured slog access log. Fields: `request_id`, `method`, `path`, `status`, `bytes`, `latency_ms`, `remote_ip`. Sampled to 10% on 2xx after 7d (per `[telemetry]`).
- `metrics`: `http_requests_total{role,route,code}` counter, `http_request_duration_seconds` histogram with default buckets (50ms..10s).
- `cors`: allow `Origin` ∈ {`https://app.maktaba.app`, `https://web.maktaba.app`, `https://admin.maktaba.app`, `https://maktaba.app`} only; preflight cached 600s.

## 5. Migrator

```go
// cloud/internal/db/migrator.go
type Migrator struct{ pool *pgxpool.Pool; dir embed.FS }

func (m *Migrator) Up(ctx context.Context) error {
    db := stdlib.OpenDB(*m.pool.Config().ConnConfig)
    defer db.Close()
    goose.SetBaseFS(m.dir)
    if err := goose.SetDialect("postgres"); err != nil { return err }
    return goose.UpContext(ctx, db, "migrations")
}

func (m *Migrator) AtHead(ctx context.Context) (bool, error) { ... }
```

Goose `lock_id = 8472612` is documented in `cloud/migrations/README.md`. Two operators running `up` simultaneously serialize on `pg_advisory_lock(8472612)`.

CLI subcommand:
- `maktaba-cloud migrate up`
- `maktaba-cloud migrate down 1`
- `maktaba-cloud migrate status`

## 6. Graceful shutdown

`server/shutdown.go`:

1. Listen for `SIGTERM`/`SIGINT`.
2. Mark `readyz` 503 immediately (LB de-pools within next probe ~10s).
3. Wait `shutdown_grace` (20s default) for in-flight requests; cancel via `http.Server.Shutdown`.
4. Close DB pool.
5. Exit 0 (or 1 if pool drain errored).

`relay` and `worker` roles override with their own drain logic in later stories.

## 7. Test plan

### 7.1 Unit

| Test | Pins |
|---|---|
| `TestConfigValidateMissingDatabase` | Missing `[database]` → exit code 2 with clear message. |
| `TestRequestIDMint` | Empty header → fresh UUIDv7 minted; non-UUIDv7 → replaced. |
| `TestRequestIDPropagate` | Existing valid `X-Request-Id` reused. |
| `TestHealthzShape` | Body has required keys (`status`, `version`, `commit`, `role`). |
| `TestReadyzDownIfDBClosed` | Closed pool → 503; `db_up` gauge = 0. |
| `TestMigratorAtHead` | Apply migrations to head; verify reports `true`. |
| `TestMigratorReversible` | `goose up` then `goose down 1` (placeholder migration) round-trips with no data loss. |

### 7.2 Integration (docker-compose Postgres in CI)

| Test | Pins |
|---|---|
| `TestEnvOverridesTOML` | Set `MAKTABA_CLOUD_DB_URL`; observe binder uses env value. |
| `TestStartFailsOnInvalidDBURL` | `MAKTABA_CLOUD_DB_URL=invalid://` → process exits non-zero within 5s with `db unreachable`. |
| `TestMetricsExposes` | `/metrics` exposes `http_requests_total`, `go_*`, `db_pool_in_use`. |
| `TestRequestIDInLogs` | Issue 100 RPS for 30s; verify every log line has matching `request_id`. |
| `TestWorkerRoleHasNoHTTP` | Start `--role=worker`; `:8080/healthz` is 404 on listener (worker only exposes 9090 control). |
| `TestRoleRequired` | Start without `--role` → exit code 2, stderr contains `--role required`. |

### 7.3 Smoke (post-deploy)

- `curl https://api.maktaba.app/healthz` → 200 + valid TLS chain.
- `curl -H 'Host: cloud.maktaba.app' https://<lb-ip>/healthz` → 200.

## 8. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Concurrent `goose up` on two pods | `pg_advisory_lock(8472612)` serializes. | `cloud/migrations/README.md`. |
| DB cold-start storm | Pool max = 25 conn/pod × 4 pods = 100 vs PG cap 200. | Config defaults. |
| TZ drift between pods | All `time.Now()` via `clock.Clock`; container env `TZ=UTC`. | `clock/clock.go`. |
| Log volume at 100 RPS | Sample 10% on 2xx after 7d; always full on 4xx/5xx. | `middleware/logging.go`. |
| SIGTERM during migration | Migration runs in single txn; SIGTERM rolls it back; next start retries. | `migrator.go`. |
| Secrets in TOML | Lint refuses non-placeholder values in `[secret]`-shaped keys. CI step: `make lint-config`. | `Makefile`. |
| IPv6-only LB | Cloudflare proxy bridges v4↔v6; documented. | `cloud/docs/runtime.md`. |
| Multi-region | Schema does not encode region; v1 single-region `fsn1`. | README/architecture. |
| Missing `[oauth.google]` for api role | Hard fail; documented as required. | `validate.go`. |
| Unknown TOML keys | Logged warning; not fatal (forward-compat). | `config.go`. |

## 9. Dependencies

- None. This is the foundation; every other 25.* story depends on this.

## 10. Acceptance checklist

- [ ] `cloud/` tree exists with `cmd/maktaba-cloud/`.
- [ ] `goose up` applies slots 0001–0010 (placeholders OK for this story).
- [ ] `--role=api` answers `/healthz` 200, `/readyz` 200 (when DB up).
- [ ] `--role` missing → exit 2.
- [ ] `MAKTABA_CLOUD_DB_URL` overrides TOML.
- [ ] `X-Request-Id` minted as UUIDv7; propagated through logs.
- [ ] `/metrics` exposes `http_requests_total` + Go runtime.
- [ ] `maktaba-cloud version` prints commit + build time + go version.
- [ ] `--role=worker` has no public HTTP listener on 8080.
- [ ] Tests in §7 all pass; CI green.
