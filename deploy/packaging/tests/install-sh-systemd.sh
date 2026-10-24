#!/usr/bin/env bash
# shellcheck disable=SC2032 # the stub sudo below runs the stub functions: right here
# install.sh's systemd choice sets up a service that starts and updates itself (B6 of the
# release audit): the maktaba user and its directories, the default config, then the
# release's own `maktaba-server setup --self-managed --system` as the maktaba user (the
# release under <root>/bin/releases with maktaba-launcher) and `setup --service --system`
# for the unit that runs the launcher. The archive's unit is never installed over the
# binaries alone (it ran a launcher nothing had installed: 203/EXEC). Runs install.sh's
# functions against a scratch tree with sudo, the account tools and the binaries stubbed;
# nothing on this machine changes.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

export MAKTABA_TEST_SYS_ROOT="${work}/var/lib/maktaba"
export MAKTABA_TEST_SYS_ETC="${work}/etc/maktaba"
export MAKTABA_TEST_SYS_LOG="${work}/var/log/maktaba"
export MAKTABA_TEST_SYSTEMD_RUN="${work}/run/systemd/system"
export MAKTABA_PREFIX="${work}/usr/local/bin"
log="${work}/calls.log"

# shellcheck source=SCRIPTDIR/../install.sh
MAKTABA_INSTALL_LIB=1 . "${here}/../install.sh"
set +e

# The stubs: every privileged call is recorded and done without privilege.
sudo() {
    if [ "${1:-}" = "-u" ]; then
        echo "as $2: ${*:3}" >>"${log}"
        shift 2
    else
        echo "root: $*" >>"${log}"
    fi
    "$@"
}
getent() { return 2; }
groupadd() { echo "groupadd $*" >>"${log}"; }
useradd() { echo "useradd $*" >>"${log}"; }
chown() { :; }
install() { # install [-d] [-o U] [-g G] [-m M] SRC... DST: the copy without the owner
    local dirmode=0 args=()
    while [ $# -gt 0 ]; do
        case "$1" in
            -d) dirmode=1; shift ;;
            -o|-g|-m) shift 2 ;;
            *) args+=("$1"); shift ;;
        esac
    done
    if [ "${dirmode}" = 1 ]; then mkdir -p "${args[@]}"; else cp "${args[0]}" "${args[1]}"; fi
}

archive() { # archive <dir> [no-launcher]: an unpacked release archive with stub binaries
    local d="$1" b
    mkdir -p "${d}"
    for b in maktaba-server maktaba-launcher maktaba-api maktaba-streaming; do
        [ "${2:-}" = no-launcher ] && [ "${b}" = maktaba-launcher ] && continue
        cat >"${d}/${b}" <<EOF
#!/usr/bin/env bash
echo "${b} \$* (MAKTABA_CONFIG=\${MAKTABA_CONFIG:-})" >>"${log}"
EOF
        chmod +x "${d}/${b}"
    done
    printf '[media]\n' >"${d}/server.toml.example"
    printf '[Service]\nExecStart=/usr/bin/maktaba-launcher\n' >"${d}/maktaba-server.service"
}

fails=0
check() { # check <name> <command...>
    if "${@:2}"; then echo "ok   $1"; else echo "FAIL $1"; fails=$((fails + 1)); fi
}

# no systemd: nothing done
rm -f "${log}"
archive "${work}/a"
( MAKTABA_SYSTEMD=1 maybe_install_systemd "${work}/a" ) >/dev/null 2>&1
check "no systemd: nothing done" test ! -s "${log}"

mkdir -p "${MAKTABA_TEST_SYSTEMD_RUN}"

# declined (non-interactive default): nothing done
( MAKTABA_SYSTEMD=0 maybe_install_systemd "${work}/a" ) >/dev/null 2>&1
check "declined: nothing done" test ! -s "${log}"

# chosen: the user, the config, the release as maktaba, the unit as root; the PATH's
# maktaba-server runs the service's release (install_binaries put the archive's there)
mkdir -p "${MAKTABA_PREFIX}"
# the archive's binaries as install_binaries left them (ELF stand-ins: no shebang)
for b in maktaba-server maktaba-api maktaba-streaming; do
    printf 'ELF %s\n' "${b}" >"${MAKTABA_PREFIX}/${b}"
done
( MAKTABA_SYSTEMD=1 maybe_install_systemd "${work}/a" ) >"${work}/out.txt" 2>&1
status=$?
check "chosen: it succeeds" test "${status}" = 0
check "the maktaba user is made" grep -q "^useradd .*--home-dir ${MAKTABA_TEST_SYS_ROOT} .*maktaba$" "${log}"
check "the default config is installed" test -f "${MAKTABA_TEST_SYS_ETC}/server.toml"
check "the release is set up as maktaba, self-managed, from its own copy" \
    grep -q "^maktaba-server setup --self-managed --system --no-service --root ${MAKTABA_TEST_SYS_ROOT} --from ${MAKTABA_TEST_SYS_ROOT}/.install-[0-9]* (MAKTABA_CONFIG=${MAKTABA_TEST_SYS_ETC}/server.toml)$" "${log}"
check "...as the maktaba user" grep -q "^as maktaba: env MAKTABA_CONFIG=.* setup --self-managed" "${log}"
check "the unit that runs the launcher is written by setup, as root" \
    grep -q "^maktaba-server setup --service --system --root ${MAKTABA_TEST_SYS_ROOT} --config ${MAKTABA_TEST_SYS_ETC}/server.toml" "${log}"
check "the release is set up before the unit" \
    test "$(grep -n 'setup --self-managed' "${log}" | head -1 | cut -d: -f1)" -lt "$(grep -n 'setup --service' "${log}" | head -1 | cut -d: -f1)"
check "the archive's unit is not installed" bash -c "! grep -q 'maktaba-server.service' '${log}'"
check "the copy is removed" bash -c "! ls -d '${MAKTABA_TEST_SYS_ROOT}'/.install-* >/dev/null 2>&1"
check "it says how to start it" grep -q "sudo systemctl start maktaba-server" "${work}/out.txt"
check "the PATH's maktaba-server is the wrapper" grep -q "^ROOT=${MAKTABA_TEST_SYS_ROOT}$" "${MAKTABA_PREFIX}/maktaba-server"
check "the archive's binaries are the fallback, off the PATH" \
    bash -c "grep -q 'ELF maktaba-server' '${work}/usr/local/lib/maktaba/maktaba-server' &&
        test ! -e '${MAKTABA_PREFIX}/maktaba-api' && test ! -e '${MAKTABA_PREFIX}/maktaba-streaming'"
# the wrapper runs the release current names, else the fallback
mkdir -p "${MAKTABA_TEST_SYS_ROOT}/bin/releases/0.9.1"
printf '#!/bin/sh\necho "release 0.9.1 $*"\n' >"${MAKTABA_TEST_SYS_ROOT}/bin/releases/0.9.1/maktaba-server"
chmod +x "${MAKTABA_TEST_SYS_ROOT}/bin/releases/0.9.1/maktaba-server" "${MAKTABA_PREFIX}/maktaba-server"
echo 0.9.1 >"${MAKTABA_TEST_SYS_ROOT}/current"
check "the wrapper runs the current release" test "$("${MAKTABA_PREFIX}/maktaba-server" migrate up)" = "release 0.9.1 migrate up"
printf '#!/bin/sh\necho "fallback $*"\n' >"${work}/usr/local/lib/maktaba/maktaba-server"
chmod +x "${work}/usr/local/lib/maktaba/maktaba-server"
echo "../../etc" >"${MAKTABA_TEST_SYS_ROOT}/current"
check "a current that is a path is never followed" test "$("${MAKTABA_PREFIX}/maktaba-server" x)" = "fallback x"
rm -f "${MAKTABA_TEST_SYS_ROOT}/current"
check "no current: the installed release" test "$("${MAKTABA_PREFIX}/maktaba-server" x)" = "fallback x"
# a second run keeps the fallback (the wrapper is never moved over it)
( MAKTABA_SYSTEMD=1 maybe_install_systemd "${work}/a" ) >/dev/null 2>&1
check "a second run keeps the fallback" grep -q "fallback" "${work}/usr/local/lib/maktaba/maktaba-server"

# an archive without the launcher (an old release): refused, nothing set up
rm -f "${log}"
archive "${work}/old" no-launcher
( MAKTABA_SYSTEMD=1 maybe_install_systemd "${work}/old" ) >/dev/null 2>&1
status=$?
check "no launcher: refused" test "${status}" != 0
check "no launcher: no setup ran" bash -c "! grep -q 'setup' '${log}' 2>/dev/null"

if [ "${fails}" -gt 0 ]; then
    echo "install-sh-systemd: ${fails} failed"
    exit 1
fi
echo "install-sh-systemd: all passed"
