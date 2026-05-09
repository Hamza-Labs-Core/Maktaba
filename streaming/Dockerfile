# syntax=docker/dockerfile:1.7
# Maktaba streaming server image (Story 22.3).
#
# Two-stage build, same shape as api/Dockerfile. The streaming binary
# itself is small; the AC4 80 MiB cap leaves headroom for an optional
# bundled FFmpeg static (excluded from the size budget per AC4). For now
# FFmpeg is provided via the Mac overlay's bind mount (or a future
# linux variant); this image carries only the Go binary + healthcheck.
#
# Build context = repo root:
#
#   docker build -f streaming/Dockerfile \
#     --build-arg VERSION=... -t ghcr.io/maktaba/streaming:dev .

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

COPY streaming/go.mod ./streaming/
COPY tools/healthcheck/go.mod ./tools/healthcheck/
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd streaming && go mod download

COPY streaming ./streaming
COPY tools/healthcheck ./tools/healthcheck

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd streaming && \
    GOARCH=${TARGETARCH:-amd64} go build \
        -trimpath \
        -ldflags "-buildid= -s -w \
            -X github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version.Version=${VERSION} \
            -X github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version.Commit=${COMMIT} \
            -X github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version.BuildDate=${SOURCE_DATE_EPOCH}" \
        -o /out/maktaba-streaming .

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd tools/healthcheck && \
    GOARCH=${TARGETARCH:-amd64} go build \
        -trimpath -ldflags "-buildid= -s -w" \
        -o /out/healthcheck .

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/Hamza-Labs-Core/Maktaba"
LABEL org.opencontainers.image.title="maktaba-streaming"
LABEL org.opencontainers.image.licenses="AGPL-3.0"

COPY --from=builder /out/maktaba-streaming /usr/local/bin/maktaba-streaming
COPY --from=builder /out/healthcheck /usr/local/bin/healthcheck

ENV HEALTHCHECK_URL=http://127.0.0.1:9101/healthz

# 8081 = public streaming port; 9101 = admin port (Story 21.4).
EXPOSE 8081 9101
USER nonroot
ENTRYPOINT ["/usr/local/bin/maktaba-streaming"]
