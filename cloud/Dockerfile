# syntax=docker/dockerfile:1.7
# Maktaba Cloud image — the single `maktaba-cloud` binary that runs the
# api / relay / worker roles (selected at runtime with `--role`). This
# Dockerfile is built primarily for the RELAY role (the VPS-deployable
# leg, see deploy/cloud-relay/), but the same image serves every role.
#
# Two-stage build mirroring api/Dockerfile (Story 22.3):
#   1. golang:1.25 builder produces a static binary using the Story 22.2
#      reproducibility envelope (-trimpath, -buildid=, -s -w, -X main.*).
#   2. gcr.io/distroless/static runtime — just the binary + a tiny Go
#      healthcheck probe (distroless has no shell/curl for HEALTHCHECK).
#
# DB migrations are embedded in the binary (cloud/migrations is an
# embed.FS), so — unlike the api image — there is no migrations dir to
# copy. The cloud go.mod has no `replace` directives, so the build
# context only needs cloud/ and tools/healthcheck/.
#
# Build context is the repo root so tools/healthcheck is reachable:
#
#   docker build -f cloud/Dockerfile \
#     --build-arg VERSION=$(git describe --tags --dirty --always) \
#     --build-arg COMMIT=$(git rev-parse HEAD) \
#     --build-arg SOURCE_DATE_EPOCH=$(git log -1 --pretty=%ct) \
#     -t ghcr.io/hamza-labs-core/maktaba-cloud:dev .

ARG GO_VERSION=1.25

FROM golang:${GO_VERSION}-bookworm AS builder
WORKDIR /src

ARG VERSION=dev
ARG COMMIT=unknown
ARG SOURCE_DATE_EPOCH=0
ARG TARGETARCH

ENV CGO_ENABLED=0 \
    GOOS=linux \
    TZ=UTC \
    LANG=C.UTF-8

# Cache module downloads separately from sources so an unrelated source
# edit doesn't bust the dep layer. cloud has its own self-contained
# module; healthcheck is a tiny standalone module.
COPY cloud/go.mod cloud/go.sum ./cloud/
COPY tools/healthcheck/go.mod ./tools/healthcheck/
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd cloud && go mod download

COPY cloud ./cloud
COPY tools/healthcheck ./tools/healthcheck

# Reproducibility envelope mirrors the root Makefile (Story 22.2). The
# version vars live in `package main` of cmd/maktaba-cloud.
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd cloud && \
    GOARCH=${TARGETARCH:-amd64} go build \
        -trimpath \
        -ldflags "-buildid= -s -w \
            -X main.Version=${VERSION} \
            -X main.Commit=${COMMIT} \
            -X main.BuiltAt=${SOURCE_DATE_EPOCH}" \
        -o /out/maktaba-cloud ./cmd/maktaba-cloud

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd tools/healthcheck && \
    GOARCH=${TARGETARCH:-amd64} go build \
        -trimpath -ldflags "-buildid= -s -w" \
        -o /out/healthcheck .

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/Hamza-Labs-Core/Maktaba"
LABEL org.opencontainers.image.title="maktaba-cloud"
LABEL org.opencontainers.image.licenses="AGPL-3.0"

COPY --from=builder /out/maktaba-cloud /usr/local/bin/maktaba-cloud
COPY --from=builder /out/healthcheck /usr/local/bin/healthcheck

# 8443 = relay's default public listener. EXPOSE-only; the runtime addr
# comes from $MAKTABA_CLOUD_LISTEN_ADDR (compose sets it to :8443). The
# healthcheck probe targets the same port's /healthz (served by every
# role via server.NewRouter).
ENV HEALTHCHECK_URL=http://127.0.0.1:8443/healthz
EXPOSE 8443

# distroless `nonroot` runs as uid 65532 — no root in the final image.
USER nonroot
ENTRYPOINT ["/usr/local/bin/maktaba-cloud"]
# Default to the relay role; override with `serve --role api|worker`.
CMD ["serve", "--role", "relay"]
