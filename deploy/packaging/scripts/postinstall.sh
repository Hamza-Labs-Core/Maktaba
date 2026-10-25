#!/bin/sh
# Post-install for the maktaba-server .deb / .rpm.
#
# Runs after files are unpacked, on both fresh installs and upgrades.
# It is sh (not bash) and avoids distro-specific tooling so the one
# script works under dpkg (Debian/Ubuntu) and rpm (Fedora/RHEL/CentOS).
# Every step is idempotent — a re-run or an upgrade must be a no-op.
#
# The package managers say which it is:
#   dpkg  postinst: "configure" ""       (fresh) | "configure" "<old version>" (upgrade)
#   rpm   %post:    "1"                  (fresh) | "2" or more                 (upgrade)
set -eu

upgrade=0
case "${1:-}" in
    configure) [ -n "${2:-}" ] && upgrade=1 ;;
    1) ;;
    [2-9]|[1-9][0-9]*) upgrade=1 ;;
esac

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
# What an upgrade restarts the service for (step 5): `current` moved to this package's
# release, or this package's launcher speaks another launcher_protocol than the one the
# service runs (the copy the previous package put at the root, run as the maktaba user: it
# is that user's file). The release the updater installed in its window, already current,
# is never restarted for (a package that comes after it changes nothing that runs).
launcher_protocol() {
    [ -x "$1" ] || return 0
    runuser -u "${MAKTABA_USER}" -- "$1" --version 2>/dev/null |
        sed -n 's/.*(launcher_protocol \([0-9][0-9]*\)).*/\1/p'
}
current_before=$(cat "${DATA_DIR}/current" 2>/dev/null || true)
protocol_before=$(launcher_protocol "${DATA_DIR}/maktaba-launcher")
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

# 4. No migrations here: the release's own start migrates, in its database step (a dump
#    first, put back if they fail, and the record a rollback needs: Epic 35 phase B).
#    Migrating from the package skipped all of that, and left a database a rollback to the
#    release before would refuse, with no dump to put back.

# 5. A fresh install: enable (but do not start) the service so it survives a reboot; the
#    operator edits ${CONFIG} (media roots!) then `systemctl start`. An upgrade: the
#    service starts again if it was running (try-restart), on the release `current` names
#    now, only when that moved or the launcher's protocol changed (above); otherwise what
#    runs stays running (an apt or dnf upgrade after the updater installed that release in
#    its quiet window cuts no one off). The operator's choice to enable or disable it stays
#    as it was.
if command -v systemctl >/dev/null 2>&1; then
    if [ "${upgrade}" -eq 1 ]; then
        current_after=$(cat "${DATA_DIR}/current" 2>/dev/null || true)
        protocol_after=$(launcher_protocol /usr/bin/maktaba-launcher)
        if [ "${current_after}" != "${current_before}" ] || [ "${protocol_after}" != "${protocol_before}" ]; then
            systemctl try-restart maktaba-server.service >/dev/null 2>&1 || true
            echo "maktaba-server: upgraded; the service started again if it was running (release ${current_after:-none})."
        else
            echo "maktaba-server: upgraded; the service keeps running release ${current_after:-none} (nothing it runs changed)."
        fi
        exit 0
    fi
    systemctl enable maktaba-server.service >/dev/null 2>&1 || true
fi
if [ "${upgrade}" -eq 1 ]; then
    exit 0
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
