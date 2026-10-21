#!/bin/sh
# The Linux app's package removal (Epic 39 story 39.10): the .deb's prerm and the .rpm's
# %preun (payload.sh's tauri overlay names this file). The server does not run from the
# package: `maktaba-server service install` wrote a systemd user unit that runs each user's
# runtime copy (~/.local/share/maktaba/runtime). Removing the app stops and disables that
# unit for every user whose user manager runs (logged in, or lingering: the server at boot),
# so nothing of the app keeps running after it is gone. The data, the runtime copy and the
# unit file stay in the user's home: reinstalling the app, or `maktaba-server service
# install`, brings the same server back.
#
# Only a removal does this, never an upgrade: dpkg calls prerm with "remove" (and
# "upgrade" on an upgrade), rpm calls %preun with the number of instances left (0 on an
# erase, 1 on an upgrade). A user whose manager is not running keeps the unit enabled; the
# app's docs say how to remove it (`systemctl --user disable --now maktaba-server`). Errors
# never fail the removal.
set -u

case "${1:-}" in
    remove | 0) ;;
    *) exit 0 ;;
esac
command -v systemctl >/dev/null 2>&1 || exit 0
[ -d /run/systemd/system ] || exit 0 # systemd is not the init: no user managers

unit=maktaba-server.service
# The first line `maktaba-server service install` writes (cmd/maktaba-server/internal/
# service/appunit.go, appMarker): a unit of that name without it belongs to another
# install (an Epic 35 user unit) and is left alone. The backquotes are the marker's own.
# shellcheck disable=SC2016
marker='# Maktaba (Epic 39): written by `maktaba-server service install`'

for dir in /run/user/*; do
    uid="${dir##*/}"
    case "$uid" in '' | *[!0-9]*) continue ;; esac
    [ -S "$dir/systemd/private" ] || continue # no user manager runs for this uid
    user="$(getent passwd "$uid" | cut -d: -f1)"
    [ -n "$user" ] || continue
    # the unit file that user's manager loaded, wherever its XDG_CONFIG_HOME puts it
    path="$(systemctl --user --machine="$user@" show --property=FragmentPath --value "$unit" 2>/dev/null)" || continue
    if [ -z "$path" ] || [ ! -f "$path" ]; then continue; fi
    first=""
    IFS= read -r first <"$path" 2>/dev/null || [ -n "$first" ] || continue
    case "$first" in "$marker"*) ;; *) continue ;; esac
    if systemctl --user --machine="$user@" disable --now "$unit" >/dev/null 2>&1; then
        echo "Maktaba: stopped and disabled the server of $user (its data stays in its home)"
    else
        echo "Maktaba: could not stop the server of $user; they can run: systemctl --user disable --now $unit" >&2
    fi
done
exit 0
