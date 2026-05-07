# Story 25.30 — Official Docker image

> Epic 25 · Cloud relay · Phase 6 (server distribution)

## Description

The official multi-arch Docker image is the canonical install for
Linux self-hosters who want isolation, reproducibility, and the
power of compose to manage Postgres + Maktaba together.

Image:

- **Registry:** `ghcr.io/hamza-labs-core/maktaba:<tag>` (also
  `:latest` for stable, `:edge` for nightly).
- **Architectures:** `linux/amd64`, `linux/arm64/v8`.
- **Base:** `debian:bookworm-slim` for glibc compatibility with
  Whisper / FFmpeg; **not** Alpine (musl breaks PyTorch).
- **Multi-stage build:**
  - Stage 1: Go builder produces `api`, `streaming` static
    binaries.
  - Stage 2: Python builder uses `uv` to materialize a venv
    pinned to `requirements.lock`.
  - Stage 3: runtime copies the venv, Go binaries, and a
    pinned-version FFmpeg static build.
- **Image size target:** < 1.5 GB compressed (most of which is
  Whisper model weights, optionally separate volume).
- **Non-root user.** `UID 10001 maktaba`. Container runs as
  this user.

`docker-compose.yml` (reference, in `deploy/compose/`):

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: maktaba
      POSTGRES_PASSWORD_FILE: /run/secrets/pg_password
      POSTGRES_DB: maktaba
    volumes:
      - pgdata:/var/lib/postgresql/data
    secrets: [pg_password]

  maktaba:
    image: ghcr.io/hamza-labs-core/maktaba:latest
    depends_on: [postgres]
    environment:
      MAKTABA_DB_URL: postgres://maktaba@postgres:5432/maktaba?sslmode=disable
      MAKTABA_DB_PASSWORD_FILE: /run/secrets/pg_password
      TZ: ${TZ:-UTC}
    volumes:
      - /path/to/your/media:/media:ro
      - maktaba-data:/var/lib/maktaba
      - maktaba-cache:/var/cache/maktaba
    ports:
      - "8080:8080"
      - "8081:8081"
    restart: unless-stopped
    secrets: [pg_password]

volumes:
  pgdata:
  maktaba-data:
  maktaba-cache:

secrets:
  pg_password:
    file: ./secrets/pg_password.txt
```

Environment-variable config (12-factor):

- `MAKTABA_DB_URL`, `MAKTABA_DB_PASSWORD_FILE`
- `MAKTABA_LOG_LEVEL`, `MAKTABA_LOG_FORMAT=json`
- `MAKTABA_LIBRARY_ROOTS=/media/lectures:/media/films`
- `MAKTABA_TRANSCRIBE_DEVICE=cpu|cuda|mlx`
- `MAKTABA_CLOUD_LINK_TOKEN_FILE=/run/secrets/cloud_link`
- `MAKTABA_PUBLIC_URL=https://maktaba.local`

## Acceptance criteria

- **Given** a user runs `docker compose up -d` from the
  reference compose,
  **when** containers start,
  **then** within 60s `curl http://localhost:8080/healthz`
  returns 200.
- **Given** the image is pulled on Apple Silicon,
  **when** Docker Desktop runs it,
  **then** the `arm64/v8` variant is selected automatically
  and the container runs natively (no QEMU).
- **Given** the user mounts `/media` read-only,
  **when** Maktaba scans,
  **then** sidecars (subtitles, thumbnails) are written to
  `/var/cache/maktaba`, never to `/media`.
- **Given** the container restarts,
  **when** SIGTERM is received,
  **then** subprocesses drain ≤ 30s and exit cleanly with
  code 0.
- **Given** the user supplies `MAKTABA_DB_PASSWORD_FILE`,
  **when** Maktaba starts,
  **then** it reads the secret from the file (Docker
  secrets pattern) and never logs it.
- **Given** the user upgrades by `docker compose pull && up
  -d`,
  **when** the new image starts,
  **then** schema migrations apply automatically and the
  service resumes.
- **Given** the user mounts a media folder that the
  container's UID can't read,
  **when** the scanner starts,
  **then** the error log makes the cause obvious
  ("EACCES /media/lectures — chown to UID 10001 or fix
  permissions") and the service degrades gracefully.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | CI          | build amd64 + arm64 | publish manifest list | both arches push |
| T02 | smoke       | run amd64 on Linux | compose up | healthz 200 |
| T03 | smoke       | run arm64 on Mac | compose up | healthz 200 |
| T04 | regression  | upgrade with running scan | pull + recreate | resumes |
| T05 | unit        | image scan (Trivy) | run | 0 high CVEs |
| T06 | regression  | non-root verification | `whoami` in container | `maktaba` |
| T07 | integration | env-var only config | start without TOML | works |
| T08 | regression  | mount perm error | scan | clear error in log |
| T09 | smoke       | Podman compatibility | `podman compose up` | works |
| T10 | unit        | image size | inspect | < 1.5 GB compressed |

## Edge cases

- **MLX in Docker on Mac.** Docker Desktop on macOS runs
  Linux containers; MLX needs the host. CPU Whisper falls
  back automatically. Documented; recommend native install
  on macOS for transcription speed (25.27).
- **GPU in Docker on Linux.** Optional `--gpus all` enables
  CUDA Whisper; we publish a `cuda` tag with PyTorch CUDA
  wheel preinstalled (separate, larger image).
- **Volume permissions.** UID 10001 mismatch with host UID
  is a common gotcha. We document the `--user "$(id -u):$(id -g)"`
  override, and the image accepts arbitrary UIDs (uses
  `/etc/passwd` mutable via `nss_wrapper` only if needed).
- **Postgres on the host.** Some users prefer their own PG;
  we document `MAKTABA_DB_URL=postgres://...` to point at it.
- **SQLite mode.** Single-user, file-on-volume; suitable
  for hobbyists. Set `MAKTABA_DB_URL=sqlite:///var/lib/maktaba/maktaba.db`.
- **Container-first vs systemd-first.** Mutually exclusive
  on a single host (port collisions). Document.
- **Health check.** `HEALTHCHECK` directive points at
  `/healthz`. Compose surfaces unhealthy state.
- **TZ.** Default UTC; `TZ` env exported to all
  subprocesses for log timestamps.
- **Image trust.** Sigstore cosign-signed; we publish
  the public key in our docs.
- **Air-gapped deployments.** `docker save` + `docker load`
  works; document offline workflow.

## Files / packages

- `Dockerfile` — multi-stage.
- `deploy/compose/docker-compose.yml`.
- `deploy/compose/docker-compose.cuda.yml` overlay.
- `deploy/compose/docker-compose.dev.yml` overlay (live
  reload).
- `release/.github/workflows/docker.yml` — buildx + cosign.

## Open questions

- **arm/v7 (32-bit).** Out — modern Pis run 64-bit; saves
  build time.
- **Helm chart.** Out for v1; user demand will drive
  inclusion.
