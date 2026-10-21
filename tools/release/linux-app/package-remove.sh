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
# keeps the unit enabled: the script names them, and a user whose manager it cannot reach
# but whose home holds the app's unit, with the command that disables it. It reads a home
# only on a local file system: one that is automounted or on the network is never touched
# while the package manager waits (a mount, a hung server), and is mentioned instead.
# Errors never fail the removal.
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

# local_home <dir>: the file system the dir is on is a local disk's, read from the mount
# table (reading it mounts nothing); an automount point or a network file system is not
local_home() {
    case "$1" in /*) ;; *) return 1 ;; esac
    fs="$(awk -v p="$1" '
        { mp = $2; gsub(/\\040/, " ", mp) }
        (p == mp || index(p, (mp == "/" ? "" : mp) "/") == 1) && length(mp) >= best { best = length(mp); t = $3 }
        END { print t }' /proc/self/mounts 2>/dev/null)"
    case "$fs" in
        ext2 | ext3 | ext4 | xfs | btrfs | zfs | f2fs | bcachefs | jfs | reiserfs | nilfs2 | tmpfs | overlay) return 0 ;;
    esac
    return 1
}

# app_unit_in <home>: the app's unit in the home's default unit directory (local homes only)
app_unit_in() {
    local_home "$1" && is_app_unit "$1/.config/systemd/user/$unit"
}

# is_app_unit <file>: the file's first line is the app's marker
is_app_unit() {
    [ -f "$1" ] || return 1
    first=""
    IFS= read -r first 2>/dev/null <"$1" || [ -n "$first" ] || return 1
    case "$first" in "$marker"*) return 0 ;; esac
    return 1
}

handled=" "
for dir in /run/user/*; do
    uid="${dir##*/}"
    case "$uid" in '' | *[!0-9]*) continue ;; esac
    [ -S "$dir/systemd/private" ] || continue # no user manager runs for this uid
    entry="$(getent passwd "$uid")"
    user="${entry%%:*}"
    [ -n "$user" ] || continue
    home="$(echo "$entry" | cut -d: -f6)"
    handled="$handled$user "
    # the unit file that user's manager loaded, wherever its XDG_CONFIG_HOME puts it
    if ! path="$(systemctl --user --machine="$user@" show --property=FragmentPath --value "$unit" 2>/dev/null)"; then
        if app_unit_in "$home"; then
            echo "Maktaba: the server of $user may still be running (their user manager could not be reached); they can stop it with: systemctl --user disable --now $unit" >&2
        fi
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

# Users whose manager does not run (not logged in, no linger): the app's unit in their
# home's default unit directory (one in another XDG_CONFIG_HOME is not seen without the
# manager), on local homes only.
uid_min="$(awk '$1 == "UID_MIN" { print $2 }' /etc/login.defs 2>/dev/null)"
case "$uid_min" in '' | *[!0-9]*) uid_min=1000 ;; esac
uid_max="$(awk '$1 == "UID_MAX" { print $2 }' /etc/login.defs 2>/dev/null)"
case "$uid_max" in '' | *[!0-9]*) uid_max=60000 ;; esac
remote=0
while IFS=: read -r user _ uid _ _ home _; do
    case "$uid" in '' | *[!0-9]*) continue ;; esac
    if [ "$uid" -lt "$uid_min" ] || [ "$uid" -gt "$uid_max" ]; then continue; fi
    case "$handled" in *" $user "*) continue ;; esac
    if ! local_home "$home"; then
        remote=1
    elif is_app_unit "$home/.config/systemd/user/$unit"; then
        echo "Maktaba: the server of $user stays enabled: it starts at their next login unless they run: systemctl --user disable --now $unit" >&2
    fi
done <<EOF
$(getent passwd 2>/dev/null)
EOF
if [ "$remote" = 1 ]; then
    echo "Maktaba: homes on the network or automounted were not looked at; a user there whose server Maktaba runs keeps it enabled until they run: systemctl --user disable --now $unit" >&2
fi
exit 0
