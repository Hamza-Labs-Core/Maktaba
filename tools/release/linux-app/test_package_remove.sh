#!/usr/bin/env bash
# Tests package-remove.sh (the .deb's prerm, the .rpm's %preun) without touching the
# machine: in a user and mount namespace with a private /run, fake user managers (the
# sockets systemd keeps in /run/user/<uid>/systemd), and a fake systemctl and getent on
# PATH that record what the script asks for.
#
# Usage: test_package_remove.sh   (needs unshare from util-linux and python3)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ "${PKGTEST_IN_NS:-}" != 1 ]; then
    exec env PKGTEST_IN_NS=1 unshare --user --map-root-user --mount bash "$0" "$@"
fi

mount -t tmpfs tmpfs /run
work="$(mktemp -d)"
trap 'umount "$work/home/user1008" 2>/dev/null; rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/home"
log="$work/systemctl.log"
: >"$log"

# shellcheck disable=SC2016 # the backquotes are the marker's own
marker='# Maktaba (Epic 39): written by `maktaba-server service install`; it is replaced on every install.'

# user1000: the app's unit, manager running -> stopped and disabled
# user1001: another install's unit of that name, manager running -> left alone
# user1002: the app's unit, no manager running -> named, with the command to run
# user1003: manager running, no unit -> left alone
# user1004: the app's unit, manager running, the disable fails -> reported, removal goes on
# user1005: manager running but not reachable (show fails), no app unit -> not named
# user1006: no manager, no unit -> not named
# user1007: manager running but not reachable, the app's unit in its home -> named
# user1008: no manager, the app's unit, the home on a file system that is not a local
#           disk's (ramfs standing in for NFS or an automount) -> never read, mentioned
for u in 1000 1001 1002 1003 1004 1005 1006 1007 1008; do
    mkdir -p "/run/user/$u" "$work/home/user$u"
done
mkdir -p /run/user/notauid
for u in 1000 1001 1003 1004 1005 1007; do
    mkdir -p "/run/user/$u/systemd"
    python3 -c 'import socket, sys; socket.socket(socket.AF_UNIX).bind(sys.argv[1])' "/run/user/$u/systemd/private"
done
for u in 1000 1002 1004; do
    printf '%s\n[Unit]\n' "$marker" >"$work/home/user$u/maktaba-server.service"
done
printf '# written by an Epic 35 install\n[Unit]\n' >"$work/home/user1001/maktaba-server.service"
# where user1002's manager would load it from: the home's default unit directory
mkdir -p "$work/home/user1002/.config/systemd/user"
cp "$work/home/user1002/maktaba-server.service" "$work/home/user1002/.config/systemd/user/"
mount -t ramfs ramfs "$work/home/user1008"
for u in 1007 1008; do
    mkdir -p "$work/home/user$u/.config/systemd/user"
    printf '%s\n[Unit]\n' "$marker" >"$work/home/user$u/.config/systemd/user/maktaba-server.service"
done

cat >"$work/bin/getent" <<EOF
#!/bin/sh
[ "\$1" = passwd ] || exit 2
entry() { echo "user\$1:x:\$1:\$1::$work/home/user\$1:/bin/sh"; }
case "\${2:-all}" in
    all)
        echo "root:x:0:0::/root:/bin/sh"
        for u in 1000 1001 1002 1003 1004 1005 1006 1007 1008; do entry "\$u"; done
        echo "nobody:x:65534:65534::/nonexistent:/usr/sbin/nologin" ;;
    100[0-8]) entry "\$2" ;;
    *) exit 2 ;;
esac
EOF
cat >"$work/bin/systemctl" <<EOF
#!/bin/sh
echo "\$*" >>"$log"
user=""
for a in "\$@"; do case "\$a" in --machine=*@) user="\${a#--machine=}"; user="\${user%@}" ;; esac; done
case "\$*" in
    *" show "*)
        case "\$user" in user1005 | user1007) exit 1 ;; esac
        [ -f "$work/home/\$user/maktaba-server.service" ] && echo "$work/home/\$user/maktaba-server.service"
        exit 0 ;;
    *" disable "*)
        [ "\$user" = user1004 ] && exit 1
        exit 0 ;;
esac
exit 0
EOF
chmod +x "$work/bin/getent" "$work/bin/systemctl"
export PATH="$work/bin:$PATH"

fails=0
check() { # name, condition result
    if [ "$2" = 0 ]; then echo "  ok    $1"; else echo "  FAIL  $1"; fails=$((fails + 1)); fi
}
run() { : >"$log"; sh "$SCRIPT_DIR/package-remove.sh" "$@" >"$work/out" 2>"$work/err"; }

# not systemd as init: nothing
run remove
check "no /run/systemd/system: systemctl not called" "$(if [ -s "$log" ]; then echo 1; else echo 0; fi)"

mkdir -p /run/systemd/system
for arg in upgrade 1 failed-upgrade deconfigure ""; do
    run "$arg"
    check "an upgrade ($arg): systemctl not called" "$(if [ -s "$log" ]; then echo 1; else echo 0; fi)"
done

for arg in remove 0; do
    rc=0
    run "$arg" || rc=$?
    check "$arg: exits 0" "$rc"
    check "$arg: user1000's app unit disabled and stopped" \
        "$(grep -qx -- '--user --machine=user1000@ disable --now maktaba-server.service' "$log"; echo $?)"
    check "$arg: user1001's other unit left alone" "$(! grep -q 'user1001@ disable' "$log"; echo $?)"
    check "$arg: user1002 (no manager) not asked" "$(! grep -q 'user1002@' "$log"; echo $?)"
    check "$arg: user1002 (not logged in) named with the command" \
        "$(grep -q 'server of user1002 stays enabled: it starts at their next login unless they run: systemctl --user disable --now maktaba-server.service' "$work/err"; echo $?)"
    check "$arg: user1005 (manager not reached, no app unit) not named" "$(! grep -q 'user1005' "$work/err"; echo $?)"
    check "$arg: user1007 (manager not reached, the app's unit) named with the command" \
        "$(grep -q 'server of user1007 may still be running (their user manager could not be reached); they can stop it with: systemctl --user disable --now' "$work/err"; echo $?)"
    check "$arg: user1008 (home not on a local disk) not named" "$(! grep -q 'user1008' "$work/err"; echo $?)"
    check "$arg: homes not looked at are mentioned" \
        "$(grep -q 'homes on the network or automounted were not looked at' "$work/err"; echo $?)"
    check "$arg: user1006 (no unit), root and nobody not named" "$(! grep -qe 'user1006' -e 'root' -e 'nobody' "$work/err"; echo $?)"
    check "$arg: user1003 (no unit) not disabled" "$(! grep -q 'user1003@ disable' "$log"; echo $?)"
    check "$arg: a failed disable is reported" "$(grep -q 'user1004' "$work/err"; echo $?)"
    check "$arg: the stopped user is told about" "$(grep -q 'server of user1000' "$work/out"; echo $?)"
done

# no /proc (a chroot, an image being built): the running managers are still handled, no
# home is looked at and nothing is said about network homes (last: it hides /proc)
mount -t tmpfs tmpfs /proc
rc=0
run remove || rc=$?
check "no /proc: exits 0" "$rc"
check "no /proc: user1000's app unit still disabled" \
    "$(grep -qx -- '--user --machine=user1000@ disable --now maktaba-server.service' "$log"; echo $?)"
check "no /proc: no home looked at, no network-homes line" \
    "$(! grep -qe 'user1002' -e 'homes on the network' "$work/err"; echo $?)"

if [ "$fails" -ne 0 ]; then
    echo "$fails check(s) failed"
    exit 1
fi
echo "package-remove.sh: all checks passed"
