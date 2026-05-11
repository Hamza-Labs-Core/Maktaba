# Multi-stage build of the maktaba-server on-prem image. We build the
# binary statically against musl-friendly Go (CGO disabled) so the
# final layer is tiny — ~25 MB compressed.
ARG GO_VERSION=1.23
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

# Cache deps separately from sources so dependency-only diffs reuse layers.
COPY api/go.mod api/go.sum ./api/
COPY shared/ ./shared/
RUN cd api && go mod download

COPY api/ ./api/
ARG VERSION=dev
ARG COMMIT=unknown
RUN cd api && CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT}" \
    -o /out/maktaba-server .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/maktaba-server /usr/bin/maktaba-server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/bin/maktaba-server"]
CMD ["--config", "/etc/maktaba/server.toml"]
