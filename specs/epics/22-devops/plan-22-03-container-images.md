# Implementation Plan — Story 22.3 Container images and compose stack

> Companion to [story-22-03-container-images.md](story-22-03-container-images.md).
> Story states *what* and *why*; this plan states *how*.
> Image build mechanics for the Go services come from
> [Story 22.2](plan-22-02-reproducible-builds.md). Compose layout is
> dictated by [architecture.md §12.4](../../architecture.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Image registry | `ghcr.io/maktaba/{api,streaming,pipeline,web}`; multi-arch (amd64, arm64). |
| Compose root | `deploy/compose/` (per architecture §12). |
| Compose files | `docker-compose.yml` (canonical), `docker-compose.mac.yml` (overlay), `docker-compose.dev.yml` (overlay, owned by Story 22.8). |
| Web image | Caddy serving the static `web/dist`; not a Vite dev server. |
| Doctor | `maktaba-pipeline doctor` already exists per architecture §12.3; this story extends it with a "Mac MLX bind verified" check. |
| Out of scope | Reproducibility flags (Story 22.2); image signing (22.2); SBOM (Story 23.7). |

## 1. Architecture diagram

```
                    ┌──────────────────────────┐
   docker compose ─►│ Caddy :443               │ TLS via local-CA (Mac) or
                    │   /api → api             │ Let's Encrypt (Linux)
                    │   /graphql → api         │
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

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `deploy/compose/docker-compose.yml` | Canonical full-stack compose. |
| `deploy/compose/docker-compose.mac.yml` | Mac overlay: bind host FFmpeg, expose ANE for MLX. |
| `deploy/compose/docker-compose.dev.yml` | Dev overlay (live-reload mounts; full content owned by Story 22.8). |
| `deploy/docker/caddy/Caddyfile` | Reverse proxy + TLS termination. |
| `deploy/docker/postgres/init.sql` | DB + role bootstrap (`CREATE DATABASE maktaba`, `CREATE ROLE maktaba`). |
| `deploy/docker/healthchecks/api.sh` | Curl-against-`/api/health` script bundled in the api image. |
| `web/Dockerfile` | Two-stage build: vite build → Caddy serving dist. |
| `pipeline/.dockerignore`, `api/.dockerignore`, `streaming/.dockerignore`, `web/.dockerignore` | Reduce context. |
| `tools/image-size-guard.sh` | CI hook for AC4 (image size limits). |
| `pipeline/src/maktaba_pipeline/cli/doctor_mac.py` | The MLX bind verification probe. |

### 2.2 The compose file

`deploy/compose/docker-compose.yml`:

```yaml
name: maktaba

x-restart: &restart
  restart: unless-stopped

services:
  postgres:
    image: postgres:16-alpine@sha256:<DIGEST>
    <<: *restart
    environment:
      POSTGRES_USER: maktaba
      POSTGRES_PASSWORD_FILE: /run/secrets/pg_password
      POSTGRES_DB: maktaba
    secrets: [pg_password]
    volumes:
      - pg_data:/var/lib/postgresql/data
      - ./deploy/docker/postgres/init.sql:/docker-entrypoint-initdb.d/init.sql:ro
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U maktaba -d maktaba"]
      interval: 5s
      timeout: 3s
      retries: 10

  api:
    image: ghcr.io/maktaba/api:${MAKTABA_VERSION:-latest}
    <<: *restart
    depends_on:
      postgres: { condition: service_healthy }
    environment:
      MAKTABA_DATABASE_URL: postgres://maktaba@postgres:5432/maktaba?sslmode=disable
      MAKTABA_JWT_PRIVATE_KEY_PEM_FILE: /run/secrets/jwt_priv
      MAKTABA_JWT_PUBLIC_KEY_PEM_FILE: /run/secrets/jwt_pub
    secrets: [jwt_priv, jwt_pub]
    healthcheck:
      test: ["CMD", "/usr/local/bin/healthcheck"]  # baked-in tiny binary
      interval: 10s
      timeout: 3s
      retries: 5
    volumes:
      - media:/var/maktaba/media:ro

  streaming:
    image: ghcr.io/maktaba/streaming:${MAKTABA_VERSION:-latest}
    <<: *restart
    depends_on:
      api: { condition: service_healthy }
    environment:
      MAKTABA_API_URL: http://api:8080
      MAKTABA_JWT_PUBLIC_KEY_PEM_FILE: /run/secrets/jwt_pub
    secrets: [jwt_pub]
    volumes:
      - media:/var/maktaba/media:ro
      - cache:/var/maktaba/cache
    healthcheck:
      test: ["CMD", "/usr/local/bin/healthcheck"]
      interval: 10s

  pipeline:
    image: ghcr.io/maktaba/pipeline:${MAKTABA_VERSION:-latest}
    <<: *restart
    depends_on:
      postgres: { condition: service_healthy }
    environment:
      MAKTABA_DATABASE_URL: postgres://maktaba@postgres:5432/maktaba?sslmode=disable
    volumes:
      - media:/var/maktaba/media
      - chroma:/var/maktaba/chroma
    healthcheck:
      test: ["CMD", "maktaba-pipeline", "doctor", "--quiet"]
      interval: 30s
      timeout: 10s
      retries: 3

  web:
    image: ghcr.io/maktaba/web:${MAKTABA_VERSION:-latest}
    <<: *restart

  caddy:
    image: caddy:2-alpine@sha256:<DIGEST>
    <<: *restart
    depends_on: [api, streaming, web]
    ports: ["80:80", "443:443"]
    volumes:
      - ./deploy/docker/caddy/Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config

volumes:
  pg_data:
  media:        # Operator binds their library here.
  cache:
  chroma:
  caddy_data:
  caddy_config:

secrets:
  pg_password:
    file: ./secrets/pg_password
  jwt_priv:
    file: ./secrets/jwt_priv.pem
  jwt_pub:
    file: ./secrets/jwt_pub.pem
```

A `secrets/` directory ships with templates and `.gitignore` excluding
the real values. `make secrets-init` generates a fresh JWT keypair on
first install.

### 2.3 Caddyfile

`deploy/docker/caddy/Caddyfile`:

```
{$MAKTABA_HOSTNAME:localhost} {
    encode zstd gzip

    @api    path /api/* /graphql /ws*
    handle @api {
        reverse_proxy api:8080 {
            header_up Host {host}
            header_up X-Real-IP {remote_host}
            header_up X-Forwarded-For {remote_host}
        }
    }

    @stream path /stream/*
    handle @stream {
        reverse_proxy streaming:8081 {
            flush_interval -1            # Stream HLS segments.
            transport http {
                versions h2c h1
            }
        }
    }

    handle {
        reverse_proxy web:80
    }

    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Content-Type-Options    "nosniff"
        Referrer-Policy           "strict-origin-when-cross-origin"
    }
}
```

HSTS header is set in Story 23.3 to default-on; this plan only points
the directive at Caddy. Localhost path uses Caddy's local-CA mode.

### 2.4 Mac overlay

`deploy/compose/docker-compose.mac.yml`:

```yaml
services:
  pipeline:
    # Mac users want MLX on the Apple Neural Engine. The compose host
    # exposes the ANE through Docker Desktop's Rosetta + virtualization
    # framework; bind FFmpeg from the host's brew-installed copy.
    volumes:
      - /opt/homebrew/bin/ffmpeg:/usr/local/bin/ffmpeg:ro
      - /opt/homebrew/bin/ffprobe:/usr/local/bin/ffprobe:ro
    environment:
      MAKTABA_FFMPEG_PATH: /usr/local/bin/ffmpeg
      MAKTABA_STT_BACKEND: whisper_mlx
    # `:cached` consistency for the media volume (EC1).
    volumes:
      - type: bind
        source: ${MAKTABA_MEDIA_ROOT:-${HOME}/Movies/Maktaba}
        target: /var/maktaba/media
        consistency: cached
        read_only: true
```

`docker-compose.mac.yml` is selected via `docker compose -f
docker-compose.yml -f docker-compose.mac.yml up`. A `make compose-mac`
shortcut exists.

### 2.5 Web image

`web/Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1.7
FROM node:20-alpine@sha256:<DIGEST> AS build
ENV CI=true
WORKDIR /src
COPY web/package.json web/pnpm-lock.yaml ./
RUN --mount=type=cache,target=/pnpm-store \
    corepack enable && pnpm config set store-dir /pnpm-store && pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM caddy:2-alpine@sha256:<DIGEST>
COPY --from=build /src/dist /usr/share/caddy
COPY web/docker-Caddyfile /etc/caddy/Caddyfile
```

`web/docker-Caddyfile` is a thin SPA fallback (`try_files` to
`index.html`); the outer Caddy at the compose edge already terminates
TLS. Image size measured against the 30 MiB target in the size guard.

### 2.6 Image size guard

`tools/image-size-guard.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
declare -A MAX
MAX[api]=62914560        # 60 MiB
MAX[streaming]=83886080  # 80 MiB
MAX[pipeline]=1288490188 # 1.2 GiB
MAX[web]=31457280        # 30 MiB

for svc in api streaming pipeline web; do
  size=$(docker image inspect "ghcr.io/maktaba/${svc}:${VERSION:-latest}" --format '{{.Size}}')
  if (( size > MAX[$svc] )); then
    delta=$(( size - MAX[$svc] ))
    printf "FAIL %-9s size=%s max=%s overshoot=%s\n" "$svc" "$size" "${MAX[$svc]}" "$delta"
    exit 1
  fi
  printf "OK   %-9s size=%s\n" "$svc" "$size"
done
```

Wired into `_build-artifacts.yml` after the build step; a regression
fails CI with the delta (TC3).

### 2.7 Doctor check for Mac MLX

`pipeline/src/maktaba_pipeline/cli/doctor_mac.py`:

```python
import shutil
import subprocess
from pathlib import Path

def check_mac_mlx() -> tuple[bool, str]:
    """Verifies the Mac compose overlay produced a working MLX bind.

    Runs only when MAKTABA_STT_BACKEND=whisper_mlx; otherwise skipped.
    """
    ffmpeg = shutil.which("ffmpeg") or "/usr/local/bin/ffmpeg"
    if not Path(ffmpeg).exists():
        return False, f"ffmpeg not found at {ffmpeg} — bind mount failed"

    try:
        # Single-line probe: `mlx.core.metal.is_available` returns True iff
        # the host's Metal stack is reachable from inside the container.
        out = subprocess.run(
            ["python", "-c", "import mlx.core as mx; print(mx.metal.is_available())"],
            capture_output=True, text=True, timeout=5, check=True,
        )
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as e:
        return False, f"MLX import failed: {e}"
    if "True" not in out.stdout:
        return False, "mlx.metal.is_available() returned False"
    return True, "MLX + ffmpeg bind verified"
```

Wired into `maktaba-pipeline doctor`; surfaces `mac_mlx` as a check
key. The compose-mac path's smoke test asserts this key is `OK`.

## 3. Test plan

### 3.1 Cold boot (TC1)

| Test | What it pins |
|---|---|
| `TestCompositeColdBoot` | `docker compose up -d` on a fresh runner; all five healthchecks reach `healthy` within 90 s; `curl /api/health` returns 200. |
| `TestComposeDownAndUpIsClean` | `down` then `up -d`; volumes persist; second boot ≤ 30 s. |

### 3.2 Mac overlay (TC2)

| Test | What it pins |
|---|---|
| `TestMacOverlayBootsOnDarwinARM64` | A self-hosted darwin/arm64 runner runs `docker compose -f compose -f compose.mac.yml up`; `maktaba-pipeline doctor mac_mlx` returns OK. |
| `TestMacOverlayMissingFFmpeg` | Without `/opt/homebrew/bin/ffmpeg`, the bind fails; the doctor surfaces `mac_mlx: ffmpeg not found` and pipeline starts with `whisper_cpu` fallback. |

### 3.3 Image size

| Test | What it pins |
|---|---|
| `TestApiImageSize` (TC3) | `tools/image-size-guard.sh` exits 0 for the four images on a clean build; a 1 MiB inflation in api fails with the documented delta. |
| `TestNoUselessLayers` | Final image lists no `apt cache`, `pip cache`, `node_modules` (web stage), `.git` (any stage). Asserted via `docker image inspect`. |

### 3.4 Healthchecks

| Test | What it pins |
|---|---|
| `TestApiHealthBeforeReady` | Killing postgres causes api's healthcheck to flap; compose flags api as unhealthy within 50 s. |
| `TestPipelineHealthBackoff` | `maktaba-pipeline doctor` failing 3× in a row marks pipeline unhealthy; clears within one healthy cycle after a fix. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Mac filesystem perf (EC1) | Bind mount uses `consistency: cached` for the media volume; documented in `compose.mac.yml`. | `TestMacOverlayBootsOnDarwinARM64` |
| SELinux on Linux (EC2) | Bind mount lines append `:Z` when `MAKTABA_SELINUX=1` — controlled via a `compose.selinux.yml` overlay; documented in deploy README. | `TestSelinuxOverlay` (RHEL runner) |
| Rootless Docker (EC3) | All UIDs run as `1000:1000` baked into images; volumes inherit the same UID via `userns-remap`. Documented; no extra overlay. | `TestRootlessBoot` |
| Postgres volume from host owned by root | The init script `chown -R 1000 /var/lib/postgresql/data` is skipped (Postgres image owns this); volumes default to docker-managed. Bind-mounted DB data is unsupported in v1, documented. | n/a |
| Docker Desktop on Windows | Untested in v1; doctor surfaces `unsupported platform` and the install guide points to compose on WSL2. | `TestDoctorWindowsBanner` |
| Compose API v2 vs v1 | Compose file uses v2 (no `version:` key); v1 docker-compose binary is unsupported and rejected at `make compose-up`. | `TestComposeV1Refused` |
| Image pull rate limit | `compose pull` retries with backoff; release docs include `docker login ghcr.io` instructions for authenticated pulls. | n/a |
| Caddy auto-TLS unable to reach LE on first boot | Caddy's `internal` issuer falls back to local-CA; the boot succeeds with a self-signed cert and a logged warning; reachable via `https://localhost`. | `TestCaddyFallbackInternalIssuer` |
| Stale image on upgrade | `compose pull` before `up -d` is required; documented in Story 22.6. | n/a |
| `media` volume binds outside ${HOME} | Documented in deploy README; `MAKTABA_MEDIA_ROOT` must exist and be readable by UID 1000. | `TestMediaRootMissing` |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `docker compose` | v2.27+ | Required for `--wait` flag, `secrets`, `condition: service_healthy`. |
| `caddy` | 2.x | Reverse proxy + auto-TLS. |
| `postgres` | 16-alpine | Pinned per architecture §2.1. |
| `ko` | latest | Build go images (Story 22.2). |
| `buildx` | latest | Pipeline image (provenance + sbom). |

## 6. Acceptance checklist

**Images**
- [ ] Four images published per release (api, streaming, pipeline, web).
- [ ] Multi-arch (linux/amd64, linux/arm64).
- [ ] Sizes within the AC4 caps.

**Compose**
- [ ] `docker compose up -d` brings the stack to all-healthy in ≤ 90 s.
- [ ] `docker-compose.mac.yml` overlay exists with the `:cached` bind for media and the FFmpeg/MLX hookup.
- [ ] `maktaba-pipeline doctor` reports `mac_mlx: OK` under the Mac overlay.
- [ ] Volumes named (`pg_data`, `media`, `cache`, `chroma`, `caddy_data`).

**Caddy**
- [ ] Routes `/api`, `/graphql`, `/ws`, `/stream`, `/` to the right service.
- [ ] HSTS header set; Story 23.3 owns the policy.

**CI**
- [ ] Image size guard wired into the build-artifacts gate.
- [ ] Healthcheck round-trip tested in `_e2e.yml`.
