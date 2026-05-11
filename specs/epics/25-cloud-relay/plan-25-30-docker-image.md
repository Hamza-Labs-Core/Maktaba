# Implementation Plan — Story 25.30 Official Docker image

> Companion to [story-25-30-docker-image.md](story-25-30-docker-image.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Image | `ghcr.io/hamza-labs-core/maktaba`; tags: semver + `latest` + `edge`. |
| Arches | `linux/amd64`, `linux/arm64/v8`. |
| Base | `debian:bookworm-slim`. |
| Build | Multi-stage Dockerfile in repo root. |
| Signing | Sigstore cosign; public key published in docs. |
| Size target | < 1.5 GB compressed. |
| Out of scope | armhf (32-bit) builds. Helm chart. |

## 1. Dockerfile

```dockerfile
# syntax=docker/dockerfile:1.6

# --- Stage 1: Go build ---
FROM golang:1.22-bookworm AS go-build
WORKDIR /src
COPY api/ ./api
COPY streaming/ ./streaming
COPY cmd/ ./cmd
COPY shared/ ./shared
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /out/api          ./api/cmd/api
RUN go build -trimpath -ldflags="-s -w" -o /out/streaming    ./streaming/cmd/streaming
RUN go build -trimpath -ldflags="-s -w" -o /out/pipeline-launcher ./cmd/pipeline-launcher
RUN go build -trimpath -ldflags="-s -w" -o /out/maktaba-cloudlink ./cmd/maktaba-cloudlink
RUN go build -trimpath -ldflags="-s -w" -o /out/maktaba      ./cmd/maktaba

# --- Stage 2: Python venv ---
FROM python:3.12-slim-bookworm AS py-build
WORKDIR /venv
COPY pipeline/pyproject.toml pipeline/uv.lock ./pipeline/
RUN pip install --no-cache-dir uv
RUN cd pipeline && uv sync --frozen --no-dev --python-preference only-system
# /venv/pipeline/.venv now has the pipeline deps

# --- Stage 3: FFmpeg static ---
FROM debian:bookworm-slim AS ffmpeg-fetch
RUN apt-get update && apt-get install -y --no-install-recommends curl xz-utils ca-certificates
ARG TARGETARCH
RUN set -e; \
  case "$TARGETARCH" in \
    amd64) URL=https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz ;; \
    arm64) URL=https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-arm64-static.tar.xz ;; \
  esac; \
  curl -sSL "$URL" | tar -xJ --strip-components=1 -C /tmp; \
  install -m 0755 /tmp/ffmpeg /usr/local/bin/ffmpeg; \
  install -m 0755 /tmp/ffprobe /usr/local/bin/ffprobe

# --- Stage 4: runtime ---
FROM debian:bookworm-slim AS runtime
RUN useradd -u 10001 -m -d /home/maktaba -s /usr/sbin/nologin maktaba
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates libvips42 tini && \
    rm -rf /var/lib/apt/lists/*
COPY --from=go-build /out/* /usr/local/bin/
COPY --from=py-build /venv/pipeline/.venv /opt/maktaba/python
COPY --from=ffmpeg-fetch /usr/local/bin/ffmpeg /usr/local/bin/ffprobe /usr/local/bin/
RUN install -d -o 10001 -g 10001 /var/lib/maktaba /var/cache/maktaba /var/log/maktaba /etc/maktaba
USER 10001
EXPOSE 8080 8081
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/maktaba"]
CMD ["serve"]
HEALTHCHECK --interval=30s --timeout=5s CMD wget -qO- http://127.0.0.1:8080/healthz | grep -q ok
```

## 2. Compose reference

`deploy/compose/docker-compose.yml`:

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: maktaba
      POSTGRES_PASSWORD_FILE: /run/secrets/pg_password
      POSTGRES_DB: maktaba
    volumes: [pgdata:/var/lib/postgresql/data]
    secrets: [pg_password]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U maktaba"]
      interval: 10s
      retries: 5

  maktaba:
    image: ghcr.io/hamza-labs-core/maktaba:latest
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      MAKTABA_DB_URL: postgres://maktaba@postgres:5432/maktaba?sslmode=disable
      MAKTABA_DB_PASSWORD_FILE: /run/secrets/pg_password
      TZ: ${TZ:-UTC}
    volumes:
      - /path/to/your/media:/media:ro
      - maktaba-data:/var/lib/maktaba
      - maktaba-cache:/var/cache/maktaba
    ports: ["8080:8080","8081:8081"]
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

`deploy/compose/docker-compose.cuda.yml`:

```yaml
services:
  maktaba:
    image: ghcr.io/hamza-labs-core/maktaba:cuda
    deploy:
      resources:
        reservations:
          devices:
            - capabilities: [gpu]
```

## 3. Env config

```
MAKTABA_DB_URL                  postgres://user@host:5432/db
MAKTABA_DB_PASSWORD_FILE        path to secret file
MAKTABA_LOG_LEVEL               info|debug|warn
MAKTABA_LOG_FORMAT              json|text
MAKTABA_LIBRARY_ROOTS           :-separated paths
MAKTABA_TRANSCRIBE_DEVICE       cpu|cuda|mlx (mlx N/A in Linux container)
MAKTABA_CLOUD_LINK_TOKEN_FILE   path to claim token
MAKTABA_PUBLIC_URL              public URL
MAKTABA_HTTP_PORT               8080
MAKTABA_STREAMING_PORT          8081
```

## 4. CI matrix

`.github/workflows/docker.yml`:

```yaml
jobs:
  build-and-publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with: { registry: ghcr.io, username: ${{ github.actor }}, password: ${{ secrets.GITHUB_TOKEN }} }
      - uses: docker/build-push-action@v5
        with:
          context: .
          platforms: linux/amd64,linux/arm64
          push: true
          tags: |
            ghcr.io/hamza-labs-core/maktaba:${{ github.ref_name }}
            ghcr.io/hamza-labs-core/maktaba:latest
          provenance: true
          sbom: true
      - name: cosign sign
        run: cosign sign --yes ghcr.io/hamza-labs-core/maktaba:${{ github.ref_name }}
```

## 5. Test plan

### 5.1 CI

| Test | Pins |
|---|---|
| `build_amd64_arm64_publishes_manifest_list` | Both archs pushed. |
| `trivy_no_high_cves` | Image scan clean. |
| `cosign_verify` | Signature verifies. |
| `image_size_under_1.5gb` | Compressed size measured. |

### 5.2 Smoke

| Test | Pins |
|---|---|
| `linux_amd64_compose_up` | healthz 200 in 60s. |
| `linux_arm64_compose_up_on_mac` | Docker Desktop arm64 image starts. |
| `podman_compose_up` | Same. |
| `upgrade_with_running_scan` | Pull + recreate → scan resumes (Epic 06). |
| `mount_permission_error_message` | EACCES → clear log message. |

### 5.3 Regression

| Test | Pins |
|---|---|
| `non_root_uid_in_container` | `whoami` → `maktaba`. |
| `env_only_config` | Start without TOML using env vars. |
| `air_gapped_save_load` | `docker save` + `docker load`. |

## 6. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| MLX on Mac Docker | Falls back to CPU; doc recommends native. | Spec. |
| GPU on Linux | Separate `:cuda` tag. | Compose overlay. |
| Volume UID mismatch | `--user "$(id -u):$(id -g)"` documented; image accepts arbitrary UIDs. | Doc. |
| Postgres on host | `MAKTABA_DB_URL` to host PG. | Doc. |
| SQLite mode | URL `sqlite:///var/lib/maktaba/maktaba.db`. | Doc. |
| Systemd vs container | Mutually exclusive on host. | Doc. |
| HEALTHCHECK | Built-in. | Dockerfile. |
| TZ | Default UTC. | Spec. |
| cosign trust | Public key in docs. | Doc. |
| `:edge` tag | Nightly build for adventurous users. | CI. |

## 7. Dependencies

- Shared Go + Python build pipeline.
- 25.27/25.28 share binaries (built once per CI run).

## 8. Acceptance checklist

- [ ] Multi-arch image builds + pushes.
- [ ] cosign signature; SBOM; provenance.
- [ ] HEALTHCHECK + non-root UID 10001.
- [ ] Reference compose works on Linux + Mac.
- [ ] CUDA overlay available.
- [ ] Tests in §5 pass.
