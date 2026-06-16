#!/bin/sh
# Post-remove for the maktaba-server .deb / .rpm.
#
# Reloads systemd so the removed unit disappears from the manager. The
# `maktaba` user and ${DATA_DIR} are intentionally left behind on a plain
# uninstall so a re-install keeps the operator's database and config; a
# purge / manual cleanup removes them.
set -eu

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload >/dev/null 2>&1 || true
fi

exit 0
