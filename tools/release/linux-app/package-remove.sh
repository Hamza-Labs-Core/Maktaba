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
# erase, 1 on an upgrade). A user whose manager is not running (not logged in, no linger)
# keeps the unit enabled: the script names them, and a user whose manager it cannot reach,
# with the command that disables it. Errors never fail the removal.
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

manual() { # manual <user> <why>
    echo "Maktaba: the server of $1 stays enabled ($2): it starts again at their next login unless they run: systemctl --user disable --now $unit" >&2
}

# is_app_unit <file>: the file's first line is the app's marker
is_app_unit() {
    first=""
    IFS= read -r first <"$1" 2>/dev/null || [ -n "$first" ] || return 1
    case "$first" in "$marker"*) return 0 ;; esac
    return 1
}

handled=" "
for dir in /run/user/*; do
    uid="${dir##*/}"
    case "$uid" in '' | *[!0-9]*) continue ;; esac
    [ -S "$dir/systemd/private" ] || continue # no user manager runs for this uid
    user="$(getent passwd "$uid" | cut -d: -f1)"
    [ -n "$user" ] || continue
    handled="$handled$user "
    # the unit file that user's manager loaded, wherever its XDG_CONFIG_HOME puts it
    if ! path="$(systemctl --user --machine="$user@" show --property=FragmentPath --value "$unit" 2>/dev/null)"; then
        manual "$user" "their user manager could not be reached"
        continue
    fi
    if [ -z "$path" ] || [ ! -f "$path" ]; then continue; fi
    is_app_unit "$path" || continue
    if systemctl --user --machine="$user@" disable --now "$unit" >/dev/null 2>&1; then
        echo "Maktaba: stopped and disabled the server of $user (its data stays in its home)"
    else
        echo "Maktaba: could not stop the server of $user; they can run: systemctl --user disable --now $unit" >&2
    fi
done

# Users whose manager does not run: the app's unit in their home's default unit directory
# (one in another XDG_CONFIG_HOME is not seen without the manager).
uid_min="$(awk '$1 == "UID_MIN" { print $2 }' /etc/login.defs 2>/dev/null)"
case "$uid_min" in '' | *[!0-9]*) uid_min=1000 ;; esac
getent passwd 2>/dev/null | while IFS=: read -r user _ uid _ _ home _; do
    case "$uid" in '' | *[!0-9]*) continue ;; esac
    [ "$uid" -ge "$uid_min" ] || continue
    case "$handled" in *" $user "*) continue ;; esac
    file="$home/.config/systemd/user/$unit"
    if [ -f "$file" ] && is_app_unit "$file"; then
        manual "$user" "not logged in"
    fi
done
exit 0
