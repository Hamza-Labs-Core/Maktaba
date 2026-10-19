#!/bin/sh
# /usr/bin/maktaba-server for the .deb / .rpm (Epic 35, story 35.2).
#
# The service runs /usr/bin/maktaba-launcher, which starts the current
# release under /var/lib/maktaba/bin/releases/<version>/ (the updater installs
# new ones there). This CLI runs that same release, so `maktaba-server
# migrate`, `adduser`, ... match the running server; when the release
# directory is not readable (a user outside the maktaba group) or not set up
# yet, it runs the release the package shipped.
ROOT=/var/lib/maktaba
PACKAGED=/usr/lib/maktaba/release/maktaba-server

cur=""
if [ -r "${ROOT}/current" ]; then
    cur=$(tr -d '[:space:]' <"${ROOT}/current" 2>/dev/null)
fi
case "${cur}" in
    ""|*[!0-9A-Za-z.+_-]*) cur="" ;;   # never a path
esac
if [ -n "${cur}" ] && [ -x "${ROOT}/bin/releases/${cur}/maktaba-server" ]; then
    exec "${ROOT}/bin/releases/${cur}/maktaba-server" "$@"
fi
exec "${PACKAGED}" "$@"
