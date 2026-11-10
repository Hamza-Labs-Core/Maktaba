# The Docker install of Maktaba (ghcr.io/hamza-labs-core/maktaba-server:<version>).
#
# The image is a bootstrap of the headless Linux install (specs/epics/39-linux-app/headless.md):
# it carries its release's own Linux payload (the signed maktaba-linux-payload-<v>-<arch>.tar.gz,
# unpacked), the distribution's ffmpeg and tini. Its entrypoint, `maktaba-server container`,
# puts that payload on the volume's runtime (/var/lib/maktaba/data/maktaba/runtime) when the
# runtime has none or an older one, then runs the runtime's launcher once setup is done. The
# server then updates the runtime on the volume by itself, as every headless install does: the
# release verified in process, a new runtime, `current` moved under the launcher's guard. A
# newer image pulled later goes on the volume at its first start; an older one never does.
#
# Build context: a directory holding the unpacked payloads, x86_64/server and aarch64/server
# (release.yml: the release's payload tarballs; CI: the payload it just built). An image for
# one architecture needs only that one.
#
#   docker buildx build --platform linux/amd64,linux/arm64 -f deploy/docker/server/Dockerfile <context>

ARG TARGETARCH

FROM scratch AS payload-amd64
COPY x86_64/server /server

FROM scratch AS payload-arm64
COPY aarch64/server /server

FROM payload-${TARGETARCH} AS payload

# glibc 2.36, newer than the payload's floor (tools/release/linux-app/pins.env MAX_GLIBC 2.35)
FROM debian:12-slim

ARG VERSION=dev
ARG COMMIT=unknown

# ffmpeg is the distribution's (on Linux every role runs the system's; Debian's has libx264);
# tini is PID 1: it reaps what the server's children leave and passes docker stop on;
# libnss-wrapper names a uid /etc/passwd lacks (story 59.01: the image runs as any uid:gid,
# and initdb, postgres and Python's pwd look the user up), as the official Postgres image does;
# on amd64 the VA-API drivers a NAS's chip needs (story 59.05): iHD (Broadwell and newer: J4125,
# N100; Debian's non-free component, for that package alone), i965 (older Intel), Mesa (AMD),
# and vainfo, which streaming reads the profiles with (THIRD_PARTY_NOTICES)
RUN set -eu; va=""; \
    if [ "$(dpkg --print-architecture)" = amd64 ]; then \
      printf 'Types: deb\nURIs: http://deb.debian.org/debian\nSuites: bookworm bookworm-updates\nComponents: non-free\nSigned-By: /usr/share/keyrings/debian-archive-keyring.gpg\n' \
        > /etc/apt/sources.list.d/non-free.sources; \
      va="intel-media-va-driver-non-free i965-va-driver mesa-va-drivers vainfo"; \
    fi; \
    apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
       ca-certificates ffmpeg libnss-wrapper tini tzdata $va \
    && rm -rf /var/lib/apt/lists/*

# the default user for a plain `docker run`; nothing depends on it: the image runs as whatever
# uid:gid it is started with (compose's user:, TrueNAS's Run As, Unraid's --user). The
# entrypoint refuses root, a privileged container and the Docker socket, checks that the
# volume takes a write (else it stops, exit 78, naming the folder, the uid and the fix) and,
# for a uid with no entry here, writes a passwd and group under the volume (.nss) for
# libnss_wrapper. No PUID/PGID, no root step, no chown.
RUN groupadd --gid 1000 maktaba \
    && useradd --uid 1000 --gid 1000 --home-dir /var/lib/maktaba --create-home --shell /usr/sbin/nologin maktaba

COPY --from=payload /server /opt/maktaba/server
# any uid runs the install: every path under /opt/maktaba readable (and every folder and
# executable runnable) by others, nothing there written (payload.sh gives these modes and
# files.sha256 records them; a chmod here would copy the whole payload into a second layer)
RUN bad="$(find /opt/maktaba ! -type l \( ! -perm -o=r -o -perm /go=w -o \( -type d ! -perm -o=x \) \) | head -n 5)" \
    && if [ -n "$bad" ]; then echo "not readable by every user, or writable: $bad" >&2; exit 1; fi

# every path of the install under the volume: config (server.toml), data (the runtime, the
# database), state (logs), caches
ENV HOME=/var/lib/maktaba \
    XDG_CONFIG_HOME=/var/lib/maktaba/config \
    XDG_DATA_HOME=/var/lib/maktaba/data \
    XDG_STATE_HOME=/var/lib/maktaba/state \
    XDG_CACHE_HOME=/var/lib/maktaba/cache \
    MAKTABA_CONTAINER=docker \
    LANG=C.UTF-8

LABEL org.opencontainers.image.title="maktaba-server" \
      org.opencontainers.image.description="Maktaba: the server and everything it runs, updating itself on its volume" \
      org.opencontainers.image.source="https://github.com/Hamza-Labs-Core/Maktaba" \
      org.opencontainers.image.licenses="AGPL-3.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"

USER maktaba
WORKDIR /var/lib/maktaba
VOLUME /var/lib/maktaba

# the web app (and its API), the API for the cluster's machines, live TV, the bundled
# Postgres for joined machines
EXPOSE 8088 8080 8081 54329

# docker stop: tini passes SIGTERM to the container command, which asks the launcher to stop
# (it drains the supervisor, Postgres last: give it the 90 s of compose's stop_grace_period)
ENTRYPOINT ["/usr/bin/tini", "--", "/opt/maktaba/server/bin/maktaba-server", "container"]
