# syntax=docker/dockerfile:1.7
# The cloud rehearsal's image (Epic 50 story 50.06): this commit's maktaba-cloud binary
# (the same build as cloud/Dockerfile, without the web editions the rehearsal does not
# serve) and the rehearsal's stand-ins (maktaba-rehearsal), on Debian with curl for the
# health checks. Never published.
#
#   docker build -f tools/rehearsal-cloud/Dockerfile -t maktaba-cloud:rehearsal .
ARG GO_VERSION=1.26
FROM golang:${GO_VERSION}-bookworm AS builder
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux
COPY cloud/go.mod cloud/go.sum ./cloud/
COPY shared/log/go ./shared/log/go
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd cloud && go mod download
COPY cloud ./cloud
ARG BUILD_TAGS=
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd cloud && go build -trimpath -tags "${BUILD_TAGS}" -ldflags "-s -w -X main.Version=rehearsal" -o /out/maktaba-cloud ./cmd/maktaba-cloud \
    && go build -trimpath -ldflags "-s -w" -o /out/maktaba-rehearsal ./cmd/maktaba-rehearsal

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /out/maktaba-cloud /out/maktaba-rehearsal /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/maktaba-cloud"]
CMD ["serve", "--role", "relay"]
