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

# Only an uninstall stops the service. An upgrade leaves it running: it runs the release
# under /var/lib/maktaba/bin/releases, not the package's files (a replaced
# /usr/bin/maktaba-launcher keeps running from its old inode), and the new package's
# postinstall restarts it. Stopping it here left it stopped: rpm runs the old package's
# %preun after the new one's %post.
if command -v systemctl >/dev/null 2>&1 && [ "${is_uninstall}" -eq 1 ]; then
    systemctl stop maktaba-server.service >/dev/null 2>&1 || true
    systemctl disable maktaba-server.service >/dev/null 2>&1 || true
fi

exit 0
