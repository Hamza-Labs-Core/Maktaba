# syntax=docker/dockerfile:1.7
# Maktaba's Caddy (Epic 50 story 50.02): Caddy with the DNS providers the wildcard
# certificate needs. The stock caddy image has no DNS modules, and a DNS-01 challenge is the
# only way to a wildcard (*.relay.maktaba.tv), the one certificate every node holds before
# it is needed, instead of one per home issued on demand.
#
#   dns.providers.cloudflare  production: the maktaba.tv zone's DNS-edit token
#   dns.providers.acmedns     the cloud rehearsal: a local acme-dns in front of Pebble
#
# Published by relay.yml as ghcr.io/hamza-labs-core/maktaba-caddy:<caddy>-cf<module>,
# multi-arch, cosign-signed. Versions are pinned: a bump is a pull request.
ARG GO_VERSION=1.26
ARG CADDY_VERSION=v2.11.7
ARG CLOUDFLARE_VERSION=v0.2.4
ARG ACMEDNS_VERSION=v0.7.0
ARG XCADDY_VERSION=v0.4.7

FROM golang:${GO_VERSION}-bookworm AS builder
ARG CADDY_VERSION
ARG CLOUDFLARE_VERSION
ARG ACMEDNS_VERSION
ARG XCADDY_VERSION
ENV CGO_ENABLED=0
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go install github.com/caddyserver/xcaddy/cmd/xcaddy@${XCADDY_VERSION} \
    && xcaddy build ${CADDY_VERSION} \
        --with github.com/caddy-dns/cloudflare@${CLOUDFLARE_VERSION} \
        --with github.com/caddy-dns/acmedns@${ACMEDNS_VERSION} \
        --output /out/caddy

FROM caddy:2-alpine
COPY --from=builder /out/caddy /usr/bin/caddy
RUN caddy list-modules | grep -q '^dns.providers.cloudflare$' \
    && caddy list-modules | grep -q '^dns.providers.acmedns$'
