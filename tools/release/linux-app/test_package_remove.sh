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
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/home"
log="$work/systemctl.log"
: >"$log"

# shellcheck disable=SC2016 # the backquotes are the marker's own
marker='# Maktaba (Epic 39): written by `maktaba-server service install`; it is replaced on every install.'

# user1000: the app's unit, manager running -> stopped and disabled
# user1001: another install's unit of that name, manager running -> left alone
# user1002: the app's unit, no manager running -> left alone
# user1003: manager running, no unit -> left alone
# user1004: the app's unit, manager running, the disable fails -> reported, removal goes on
for u in 1000 1001 1002 1003 1004; do
    mkdir -p "/run/user/$u" "$work/home/user$u"
done
mkdir -p /run/user/notauid
for u in 1000 1001 1003 1004; do
    mkdir -p "/run/user/$u/systemd"
    python3 -c 'import socket, sys; socket.socket(socket.AF_UNIX).bind(sys.argv[1])' "/run/user/$u/systemd/private"
done
for u in 1000 1002 1004; do
    printf '%s\n[Unit]\n' "$marker" >"$work/home/user$u/maktaba-server.service"
done
printf '# written by an Epic 35 install\n[Unit]\n' >"$work/home/user1001/maktaba-server.service"

cat >"$work/bin/getent" <<'EOF'
#!/bin/sh
[ "$1" = passwd ] || exit 2
case "$2" in 100[0-4]) echo "user$2:x:$2:$2::/home/user$2:/bin/sh" ;; *) exit 2 ;; esac
EOF
cat >"$work/bin/systemctl" <<EOF
#!/bin/sh
echo "\$*" >>"$log"
user=""
for a in "\$@"; do case "\$a" in --machine=*@) user="\${a#--machine=}"; user="\${user%@}" ;; esac; done
case "\$*" in
    *" show "*)
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
check "no /run/systemd/system: systemctl not called" "$([ ! -s "$log" ]; echo $?)"

mkdir -p /run/systemd/system
for arg in upgrade 1 failed-upgrade deconfigure ""; do
    run "$arg"
    check "an upgrade ($arg): systemctl not called" "$([ ! -s "$log" ]; echo $?)"
done

for arg in remove 0; do
    rc=0
    run "$arg" || rc=$?
    check "$arg: exits 0" "$rc"
    check "$arg: user1000's app unit disabled and stopped" \
        "$(grep -qx -- '--user --machine=user1000@ disable --now maktaba-server.service' "$log"; echo $?)"
    check "$arg: user1001's other unit left alone" "$(! grep -q 'user1001@ disable' "$log"; echo $?)"
    check "$arg: user1002 (no manager) not asked" "$(! grep -q 'user1002@' "$log"; echo $?)"
    check "$arg: user1003 (no unit) not disabled" "$(! grep -q 'user1003@ disable' "$log"; echo $?)"
    check "$arg: a failed disable is reported" "$(grep -q 'user1004' "$work/err"; echo $?)"
    check "$arg: the stopped user is told about" "$(grep -q 'server of user1000' "$work/out"; echo $?)"
done

if [ "$fails" -ne 0 ]; then
    echo "$fails check(s) failed"
    exit 1
fi
echo "package-remove.sh: all checks passed"
