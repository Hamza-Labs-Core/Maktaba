# Deployment Architecture

Pulled from architecture §11 (Configuration) and §12 (Project Structure &
Deployment), and from Epic 22 plans (Stories 22.3, 22.4, 22.7).

---

## 1. Topology

Three Go/Python service binaries, a Postgres, a Caddy reverse proxy, and
the static `web/dist` bundle served behind the same proxy. `media/`,
`cache/`, and the ChromaDB directory are bind-mounted volumes.

```
                    ┌──────────────────────────┐
   docker compose ─►│ Caddy :443               │
                    │   /api → api             │  TLS via local-CA (Mac)
                    │   /graphql → api         │  or Let's Encrypt (Linux)
                    │   /ws → api              │
                    │   /stream → streaming    │
                    │   / → web                │
                    └──────────┬───────────────┘
                               │
       ┌──────────────┬────────┴────────┬──────────────┐
       ▼              ▼                 ▼              ▼
   maktaba/api  maktaba/streaming  maktaba/pipeline  maktaba/web
   (Go, ko)     (Go, ko)           (Python, buildx)  (Caddy + dist)
       │              │                 │
       └──────────────┴─────────────────┘
                      │
                      ▼
                 ┌──────────┐
                 │ postgres │
                 └──────────┘
```

---

## 2. Service ports

| Service | Listener | Why |
|---------|----------|-----|
| API | `0.0.0.0:8080` (HTTP) | REST + GraphQL + WS, behind Caddy |
| Streaming | `0.0.0.0:8081` (HTTP) | HLS / DASH / range serving |
| Streaming gRPC | `127.0.0.1:50052` | API ↔ Streaming session RPC |
| Pipeline gRPC | `127.0.0.1:50051` | API → Pipeline (`Embed`, `Transcribe`, `Probe`, …) |
| Pipeline metrics | `:9101` | Prometheus scrape |
| Postgres | `5432` (internal) | One DB shared by all services |
| Caddy | `:80`, `:443` | TLS termination + reverse proxy |

The two gRPC listeners bind only to loopback by default. The
public-facing surface is just Caddy on 80/443.

---

## 3. The compose stack (`deploy/compose/docker-compose.yml`)

Six services + named volumes + Docker secrets:

| Service | Image | Depends on | Healthcheck |
|---------|-------|------------|-------------|
| `postgres` | `postgres:16-alpine@sha256:<digest>` | — | `pg_isready -U maktaba -d maktaba` (5 s × 10) |
| `api` | `ghcr.io/maktaba/api:${MAKTABA_VERSION}` | `postgres` healthy | tiny baked-in `/usr/local/bin/healthcheck` (10 s × 5) |
| `streaming` | `ghcr.io/maktaba/streaming:${MAKTABA_VERSION}` | `api` healthy | tiny baked-in `/usr/local/bin/healthcheck` (10 s) |
| `pipeline` | `ghcr.io/maktaba/pipeline:${MAKTABA_VERSION}` | `postgres` healthy | `maktaba-pipeline doctor --quiet` (30 s × 3) |
| `web` | `ghcr.io/maktaba/web:${MAKTABA_VERSION}` | — | (Caddy/static) |
| `caddy` | `caddy:2-alpine@sha256:<digest>` | api, streaming, web | (built-in admin) |

**Volumes.** `pg_data`, `media` (operator binds their library here),
`cache`, `chroma`, `caddy_data`, `caddy_config`.

**Secrets** (mounted as files at `/run/secrets/...`):
- `pg_password` — Postgres password.
- `jwt_priv` — RS256 private key, mounted only on `api`.
- `jwt_pub` — RS256 public key, mounted on `api` and `streaming`.

`make secrets-init` generates a fresh JWT keypair and DB password on
first install.

### Compose overlays

| File | Purpose |
|------|---------|
| `docker-compose.yml` | Canonical stack (above). |
| `docker-compose.mac.yml` | Bind-mount host FFmpeg; expose ANE / Metal for MLX transcription on the `pipeline` service. |
| `docker-compose.dev.yml` | Live-reload bind mounts of source trees (Story 22.8). |

---

## 4. Caddyfile (routing + TLS)

```
{$MAKTABA_HOSTNAME:localhost} {
    encode zstd gzip

    @api    path /api/* /graphql /ws*
    handle @api { reverse_proxy api:8080 ... }

    @stream path /stream/*
    handle @stream {
        reverse_proxy streaming:8081 {
            flush_interval -1            # stream HLS segments
            transport http { versions h2c h1 }
        }
    }

    handle { reverse_proxy web:80 }
}
```

`MAKTABA_HOSTNAME` defaults to `localhost`; on Mac it's `maktaba.local`
(mDNS, local-CA cert). On Linux self-host it's the user's domain (Let's
Encrypt).

---

## 5. Environment variables

Layered config: defaults (in code) → `/etc/maktaba/{service}.toml` →
`$MAKTABA_HOME/{service}.toml` → environment → CLI flags →
DB-stored runtime knobs.

### Top-level env
| Variable | Used by | Purpose |
|----------|---------|---------|
| `MAKTABA_HOME` | all | per-user config root override |
| `MAKTABA_VERSION` | compose | image tag pin |
| `MAKTABA_HOSTNAME` | caddy | public hostname for TLS / routing |
| `MAKTABA_DATABASE_URL` | api, pipeline | overrides `[database].url` |
| `MAKTABA_ADMIN_TOKEN` | api | bootstrap admin token (single-user mode) |
| `MAKTABA_JWT_PRIVATE_KEY_PEM` | api | RS256 private key (pem) |
| `MAKTABA_JWT_PUBLIC_KEY_PEM` | api, streaming | RS256 public key (pem) |
| `OPENAI_API_KEY` | pipeline (and per-backend equivalents) | STT API auth |

### Per-service env namespaces
- `MAKTABA_API_*` — overrides `api.toml` values.
- `MAKTABA_STREAMING_*` — overrides `streaming.toml`.
- `MAKTABA_PIPELINE_*` — overrides `pipeline.toml`.

Secrets are never logged, never returned by `/api/settings`, and never
shared between services that don't need them — Streaming never sees the
JWT private key or any STT backend keys.

---

## 6. Health checks

| Service | Endpoint / command | Used by |
|---------|--------------------|---------|
| API | tiny baked-in `healthcheck` binary curling `GET /api/health` | Compose, k8s readiness probe (future) |
| Streaming | tiny baked-in `healthcheck` binary | Compose |
| Pipeline | `maktaba-pipeline doctor --quiet` (checks ffmpeg, GPU, DB, write perms, model cache) | Compose |
| Postgres | `pg_isready -U maktaba -d maktaba` | Compose |

Caddy's own admin endpoint provides its readiness signal.

---

## 7. Startup order

`compose up` honours `depends_on` with `condition: service_healthy`:

```
postgres (healthy)
    ↓
  api  ─────►  pipeline   ──► (Pipeline workers start claiming jobs)
    ↓
streaming
    ↓
  caddy   ──► (now public)
```

`web` and `caddy` have no DB dependency and come up alongside.

The API runs `goose up` against `MAKTABA_DATABASE_URL` at boot
(architecture §12.2; Story 22.4 owns the migration runner). On a fresh
install Postgres' `init.sql` (mounted at
`/docker-entrypoint-initdb.d/init.sql`) creates the `maktaba` role and
database; goose then drives the schema forward.

---

## 8. Native install — Homebrew formula

`brew install maktaba/tap/maktaba` follows
`deploy/homebrew/Formulafile.tpl`:

```ruby
class Maktaba < Formula
  desc "Self-hosted media library with transcripts, search, and HLS streaming"
  url "{{ARCHIVE_URL}}"
  sha256 "{{ARCHIVE_SHA256}}"
  license "AGPL-3.0-or-later"
  version "{{VERSION}}"

  depends_on "ffmpeg" => :recommended
  depends_on "postgresql@16" => :recommended
  depends_on "uv"

  def install
    bin.install "bin/maktaba-api"
    bin.install "bin/maktaba-streaming"
    libexec.install "pipeline" => "pipeline"
    (bin/"maktaba-pipeline").write <<~EOS
      #!/bin/bash
      cd "#{libexec}/pipeline" && exec uv run maktaba-pipeline "$@"
    EOS
    (var/"maktaba").mkpath
    (var/"log/maktaba").mkpath
    (prefix/"Library/LaunchAgents").install Dir["launchd/io.maktaba.*.plist"]
  end

  def post_install
    # Reuse existing maktaba DB if present; otherwise bootstrap.
  end

  service do
    run [opt_bin/"maktaba-api", "serve"]
    keep_alive true
  end
end
```

Three native binaries (Go API, Go Streaming, Python pipeline as a
`uv`-managed venv), `/usr/local/var/maktaba/`, three launchd agents,
auto-started.

---

## 9. macOS launchd plists (`deploy/launchd/`)

`io.maktaba.api.plist` (analogous for streaming and pipeline):

```xml
<plist version="1.0">
<dict>
  <key>Label</key><string>io.maktaba.api</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/maktaba-api</string>
    <string>serve</string>
    <string>--config</string><string>/usr/local/etc/maktaba/api.toml</string>
  </array>
  <key>KeepAlive</key><true/>
  <key>RunAtLoad</key><true/>
  <key>StandardOutPath</key><string>/usr/local/var/log/maktaba/api.log</string>
  <key>StandardErrorPath</key><string>/usr/local/var/log/maktaba/api.err.log</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>MAKTABA_DATABASE_URL</key>
    <string>postgres:///maktaba</string>
  </dict>
</dict>
</plist>
```

`KeepAlive=true` plus `RunAtLoad=true` means launchd restarts each
service on crash and on user login.

---

## 10. Linux native install — systemd units

`deploy/packaging/systemd/maktaba-api.service`,
`maktaba-streaming.service`, `maktaba-pipeline.service`. The deb/rpm
postinst (`deploy/packaging/postinst.sh`) creates a `maktaba` system
user, sets permissions, and enables the units.

The deb/rpm metadata is generated by `nfpm` from a single
`deploy/packaging/nfpm.yaml`; binaries are published to a static apt/yum
repo at `pkg.maktaba.io/{deb,rpm}` (Story 22.7).

Multi-host scale-out (post-v1) replaces the single-host topology with N
API copies behind any L7 LB, M Streaming copies behind a sticky-session
LB (consistent hash on the `session_id` cookie), and K Pipeline copies
sharing the media volume — but Compose + systemd remains sufficient
through v1 (architecture §12.4).

---

## 11. Per-service CLIs (architecture §12.3)

```
# API Service (Go)
maktaba-api serve [--config /etc/maktaba/api.toml]
maktaba-api migrate                 # goose-driven schema migrations
maktaba-api adduser <username>      # interactive password prompt

# Streaming Service (Go)
maktaba-streaming serve [--config /etc/maktaba/streaming.toml]
maktaba-streaming probe <video_id>  # debug: dump capabilities + cached probe
maktaba-streaming gc                # one-shot cache sweep

# Pipeline Service (Python)
maktaba-pipeline serve              # gRPC server + worker pool
maktaba-pipeline worker --stages transcribe,index
maktaba-pipeline scan --library NAME
maktaba-pipeline reprocess --library NAME --from-stage transcribe
maktaba-pipeline doctor             # ffmpeg, GPU, DB, write perms, model cache
```
