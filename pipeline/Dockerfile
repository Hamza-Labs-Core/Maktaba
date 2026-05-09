# syntax=docker/dockerfile:1.7
# Maktaba pipeline image (Story 22.3).
#
# Two-stage build with uv as the resolver. Builder stage installs the
# pipeline package + its (eventual) Whisper / Chroma deps into a venv
# under /opt/venv; the final stage copies just the venv and the source
# tree onto a slim Python base.
#
# AC4 cap: 1.2 GiB. Whisper + Chroma model weights are NOT baked in —
# they download on first run into a writable cache volume so the image
# stays under the cap (Story 23.7 owns the offline-bundle variant).
#
# Mac note: when launched via docker-compose.mac.yml, host FFmpeg is
# bind-mounted at /usr/local/bin/ffmpeg and MAKTABA_STT_BACKEND is set
# to whisper_mlx. On linux this image carries its own ffmpeg from
# the distro repo; the Apple Neural Engine path is Mac-only.
#
# Build context = repo root:
#
#   docker build -f pipeline/Dockerfile -t ghcr.io/maktaba/pipeline:dev .

ARG PYTHON_VERSION=3.12

FROM python:${PYTHON_VERSION}-slim-bookworm AS builder
WORKDIR /src

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    UV_LINK_MODE=copy \
    UV_COMPILE_BYTECODE=1 \
    UV_PYTHON_DOWNLOADS=never \
    UV_PROJECT_ENVIRONMENT=/opt/venv

RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

COPY --from=ghcr.io/astral-sh/uv:0.5.11 /uv /uvx /usr/local/bin/

# Resolve and install dependencies before copying the rest of the
# source so an unrelated source edit does not invalidate the layer.
COPY pipeline/pyproject.toml pipeline/uv.lock pipeline/README.md /src/pipeline/
RUN --mount=type=cache,target=/root/.cache/uv \
    cd /src/pipeline && \
    uv sync --frozen --no-dev --no-install-project

COPY pipeline /src/pipeline
RUN --mount=type=cache,target=/root/.cache/uv \
    cd /src/pipeline && uv sync --frozen --no-dev

FROM python:${PYTHON_VERSION}-slim-bookworm
LABEL org.opencontainers.image.source="https://github.com/Hamza-Labs-Core/Maktaba"
LABEL org.opencontainers.image.title="maktaba-pipeline"
LABEL org.opencontainers.image.licenses="AGPL-3.0"

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PATH=/opt/venv/bin:$PATH \
    MAKTABA_MEDIA_ROOT=/var/maktaba/media \
    MAKTABA_CHROMA_DIR=/var/maktaba/chroma

# ffmpeg: native dependency for STT and segment extraction. The Mac
# overlay re-binds /usr/local/bin/ffmpeg over this with the host's
# brew-installed copy so users get hardware-accelerated decode.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ffmpeg ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Non-root user matches the api/streaming UID so a shared media volume
# is readable from every service (EC3 rootless docker note).
RUN groupadd --system --gid 1000 maktaba \
    && useradd --system --uid 1000 --gid maktaba --create-home --home-dir /home/maktaba maktaba \
    && mkdir -p /var/maktaba/media /var/maktaba/chroma \
    && chown -R 1000:1000 /var/maktaba

COPY --from=builder /opt/venv /opt/venv
COPY --from=builder /src/pipeline /src/pipeline

WORKDIR /src/pipeline
USER maktaba

# `maktaba-pipeline doctor --quiet` is the compose healthcheck (per the
# 22.3 plan §2.7). The CLI is not yet implemented (lands with Epic 03);
# until then we expose `python -m maktaba_pipeline` as the entrypoint
# stub so the container has a valid PID 1.
ENTRYPOINT ["python", "-m", "maktaba_pipeline"]
