#!/bin/sh
# Post-install for the maktaba-server .deb / .rpm.
#
# Runs after files are unpacked, on both fresh installs and upgrades.
# It is sh (not bash) and avoids distro-specific tooling so the one
# script works under dpkg (Debian/Ubuntu) and rpm (Fedora/RHEL/CentOS).
# Every step is idempotent — a re-run or an upgrade must be a no-op.
set -eu

MAKTABA_USER=maktaba
MAKTABA_GROUP=maktaba
DATA_DIR=/var/lib/maktaba
LOG_DIR=/var/log/maktaba
MEDIA_DIR="${DATA_DIR}/media"
CONFIG=/etc/maktaba/server.toml

# 1. Create the dedicated, unprivileged system user the unit runs as.
#    useradd ships on both Debian and RHEL families; --system gives no
#    login shell and a reserved low UID.
if ! getent group "${MAKTABA_GROUP}" >/dev/null 2>&1; then
    groupadd --system "${MAKTABA_GROUP}"
fi
if ! getent passwd "${MAKTABA_USER}" >/dev/null 2>&1; then
    useradd --system \
        --gid "${MAKTABA_GROUP}" \
        --home-dir "${DATA_DIR}" \
        --no-create-home \
        --shell /usr/sbin/nologin \
        --comment "Maktaba home server" \
        "${MAKTABA_USER}"
fi

# 2. State + log directories owned by that user (ReadWritePaths in the
#    hardened unit). The config dir stays root-owned, the file readable.
mkdir -p "${DATA_DIR}" "${MEDIA_DIR}" "${LOG_DIR}"
chown -R "${MAKTABA_USER}:${MAKTABA_GROUP}" "${DATA_DIR}" "${LOG_DIR}"
chmod 0750 "${DATA_DIR}" "${LOG_DIR}"
if [ -f "${CONFIG}" ]; then
    chmod 0644 "${CONFIG}"
fi

# 2b. Side-by-side layout (Epic 35, story 35.2 AC9): copy the packaged
#     binaries into ${DATA_DIR}/bin/releases/<version>/ and point `current`
#     at them only when this package is newer than `watermark` (a package
#     older than what the updater installed only refreshes the launcher).
#     The package owns /usr/bin/maktaba-launcher; the updater owns releases.
RELEASE_SRC=/usr/lib/maktaba/release
if [ -x "${RELEASE_SRC}/maktaba-server" ]; then
    if ! runuser -u "${MAKTABA_USER}" -- \
            "${RELEASE_SRC}/maktaba-server" setup --self-managed --system --no-service \
            --root "${DATA_DIR}" --from "${RELEASE_SRC}" --identity package; then
        echo "maktaba-server: could not stage the release under ${DATA_DIR}/bin/releases;" \
             "the service starts only once 'maktaba-server setup --self-managed' succeeds." >&2
    fi
fi

# 3. Pick up the freshly installed unit file.
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload >/dev/null 2>&1 || true
fi

# 4. Apply database migrations. Non-fatal: a host that points the config
#    at a not-yet-reachable Postgres should still finish installing and
#    can run `maktaba-server migrate up` once the DB is up. The default
#    config uses SQLite under ${DATA_DIR}, so this just works out of box.
if command -v maktaba-server >/dev/null 2>&1; then
    echo "maktaba-server: applying database migrations ..."
    if ! runuser -u "${MAKTABA_USER}" -- \
            env MAKTABA_CONFIG="${CONFIG}" maktaba-server migrate up >/dev/null 2>&1; then
        echo "maktaba-server: migrations did not complete; run" \
             "'sudo -u ${MAKTABA_USER} MAKTABA_CONFIG=${CONFIG} maktaba-server migrate up'" \
             "after configuring the database." >&2
    fi
fi

# 5. Enable (but do not start) the service so it survives a reboot. The
#    operator edits ${CONFIG} (media roots!) then `systemctl start`.
if command -v systemctl >/dev/null 2>&1; then
    systemctl enable maktaba-server.service >/dev/null 2>&1 || true
fi

cat <<EOF

Maktaba server installed.

  1. Edit your config:   sudo nano ${CONFIG}
     (set [media].roots to your library folders)
  2. Start the service:  sudo systemctl start maktaba-server
  3. Check status:       systemctl status maktaba-server
  4. Open the web UI:     http://<this-host>:8088

EOF

exit 0
