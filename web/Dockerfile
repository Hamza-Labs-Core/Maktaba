# syntax=docker/dockerfile:1.7
# Maktaba web image (Story 22.3).
#
# Two-stage build: node:20-alpine builds the static bundle into
# `web/dist`, and the final stage is caddy:2-alpine serving that bundle
# behind a SPA fallback. The outer Caddy in the compose stack
# terminates TLS; this inner Caddy only serves files. Targets the AC4
# 30 MiB cap.
#
# Build context = repo root:
#
#   docker build -f web/Dockerfile -t ghcr.io/maktaba/web:dev .

ARG NODE_VERSION=20
ARG CADDY_VERSION=2-alpine

FROM node:${NODE_VERSION}-alpine AS build
WORKDIR /src

ENV CI=true \
    PNPM_HOME=/usr/local/share/pnpm \
    PATH=/usr/local/share/pnpm:$PATH

# corepack ships with node:20 but is opt-in; enable it so we get the
# pnpm version pinned in package.json's `packageManager` field.
RUN corepack enable

COPY web/package.json web/pnpm-lock.yaml /src/
RUN --mount=type=cache,id=pnpm-store,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile

COPY web /src
RUN pnpm run build

FROM caddy:${CADDY_VERSION}
LABEL org.opencontainers.image.source="https://github.com/Hamza-Labs-Core/Maktaba"
LABEL org.opencontainers.image.title="maktaba-web"
LABEL org.opencontainers.image.licenses="AGPL-3.0"

COPY --from=build /src/dist /usr/share/caddy
COPY web/docker-Caddyfile /etc/caddy/Caddyfile

EXPOSE 80
