#!/bin/sh
# Pre-remove for the maktaba-server .deb / .rpm.
#
# Runs before files are deleted. The package managers pass different
# arguments, so we use them to tell a real uninstall apart from the
# remove-half of an upgrade and only tear the service down on a real
# uninstall:
#   dpkg  prerm:  "remove"        (uninstall)  | "upgrade <ver>" (upgrade)
#   rpm   %preun: "0"             (uninstall)  | "1"             (upgrade)
set -eu

is_uninstall=1
case "${1:-}" in
    upgrade|2) is_uninstall=0 ;;   # dpkg upgrade / rpm "still 1+ installed"
    1)         is_uninstall=0 ;;   # rpm upgrade
    0|remove|purge|"") is_uninstall=1 ;;
esac

if command -v systemctl >/dev/null 2>&1; then
    # Stop on every path so files aren't deleted under a running process;
    # the upgrade's postinstall re-enables and the operator restarts.
    systemctl stop maktaba-server.service >/dev/null 2>&1 || true
    if [ "${is_uninstall}" -eq 1 ]; then
        systemctl disable maktaba-server.service >/dev/null 2>&1 || true
    fi
fi

exit 0
