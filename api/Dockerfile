# syntax=docker/dockerfile:1.7
# Maktaba API server image (Story 22.3).
#
# Two-stage build: a fat builder produces a static Go binary using the
# reproducibility envelope from Story 22.2 (-trimpath, -buildid=, -s -w,
# -X version.*) and the final image is gcr.io/distroless/static so the
# runtime is just the binary, the migrations, and the health probe.
# Targets the AC4 60 MiB cap.
#
# Build context is the repo root so we can pull in shared/db/migrations
# without a tar trick:
#
#   docker build -f api/Dockerfile \
#     --build-arg VERSION=$(git describe --tags --dirty --always) \
#     --build-arg COMMIT=$(git rev-parse HEAD) \
#     --build-arg SOURCE_DATE_EPOCH=$(git log -1 --pretty=%ct) \
#     -t ghcr.io/maktaba/api:dev .

ARG GO_VERSION=1.23

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

# Cache deps separately from sources so an unrelated source edit does
# not invalidate the module-download layer.
COPY api/go.mod api/go.sum ./api/
COPY tools/healthcheck/go.mod ./tools/healthcheck/
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd api && go mod download

COPY api ./api
COPY shared ./shared
COPY tools/healthcheck ./tools/healthcheck

# Reproducibility envelope mirrors Makefile (Story 22.2). Keep these in
# sync with the api_ldflags variable in the root Makefile.
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd api && \
    GOARCH=${TARGETARCH:-amd64} go build \
        -trimpath \
        -ldflags "-buildid= -s -w \
            -X github.com/Hamza-Labs-Core/Maktaba/api/internal/version.Version=${VERSION} \
            -X github.com/Hamza-Labs-Core/Maktaba/api/internal/version.Commit=${COMMIT} \
            -X github.com/Hamza-Labs-Core/Maktaba/api/internal/version.BuildDate=${SOURCE_DATE_EPOCH}" \
        -o /out/maktaba-api .

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd tools/healthcheck && \
    GOARCH=${TARGETARCH:-amd64} go build \
        -trimpath -ldflags "-buildid= -s -w" \
        -o /out/healthcheck .

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/Hamza-Labs-Core/Maktaba"
LABEL org.opencontainers.image.title="maktaba-api"
LABEL org.opencontainers.image.licenses="AGPL-3.0"

COPY --from=builder /out/maktaba-api /usr/local/bin/maktaba-api
COPY --from=builder /out/healthcheck /usr/local/bin/healthcheck
COPY --from=builder /src/shared/db/migrations /var/maktaba/migrations

ENV HEALTHCHECK_URL=http://127.0.0.1:8080/healthz \
    MAKTABA_MIGRATIONS_DIR=/var/maktaba/migrations

EXPOSE 8080
USER nonroot
ENTRYPOINT ["/usr/local/bin/maktaba-api"]
