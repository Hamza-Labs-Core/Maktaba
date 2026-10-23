#!/usr/bin/env bash
# Tests apps/desktop/src-tauri/windows/installer-hooks.nsh (Epic 38, story 38.11) without
# building the app: a small NSIS script inserts the hooks around a stand-in install and
# uninstall, and stand-in Maktaba.exe programs note every run. It covers both kinds of
# Maktaba.exe, which one build of the app cannot: one that takes the --server-* flags (story
# 38.3) and one from before them (the client-only app). Which kind the installer packs is
# decided from the shell's source (knows-server-flags.ps1), so each kind is compiled with a
# source of its own: one with the flags' literals, one with them only in a comment:
#
#   a fresh install, a reinstall of the same build, an update to a build from before the
#   flags, the client-only app installed there (without a record, or with a stale one), an
#   uninstall, and the uninstall an update runs (nothing asked). Before an install the
#   previous release's pipeline wheel is removed (its name carries its version, and an
#   update installs over the old files), and nothing else of the payload.
#
# Needs makensis (tauri's copy in %LOCALAPPDATA%\tauri\NSIS after a build, or one on PATH)
# and PowerShell's Add-Type for the stand-ins. Runs in Git Bash. The records go to
# HKCU\Software\MaktabaHooksTest, removed afterwards.
#
# Usage: test-hooks.sh [--makensis PATH]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
HOOKS="$REPO_ROOT/apps/desktop/src-tauri/windows/installer-hooks.nsh"

MAKENSIS=""
while [ $# -gt 0 ]; do
    case "$1" in
        --makensis) [ $# -ge 2 ] || { echo "--makensis needs a value" >&2; exit 2; }; MAKENSIS="$2"; shift 2 ;;
        -h|--help)  sed -n '2,/^set -e/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
        *)          echo "unknown option '$1'" >&2; exit 2 ;;
    esac
done
if [ -z "$MAKENSIS" ]; then
    MAKENSIS="$(cygpath -u "${LOCALAPPDATA:-}")/tauri/NSIS/makensis.exe"
    [ -f "$MAKENSIS" ] || MAKENSIS="$(command -v makensis || true)"
fi
if [ -z "$MAKENSIS" ] || [ ! -f "$MAKENSIS" ]; then
    echo "test-hooks: no makensis (build the app once, or pass --makensis)" >&2
    exit 2
fi

KEY='HKCU\Software\MaktabaHooksTest'
wincmd() { MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*' "$@"; }
if wincmd reg.exe query "$KEY" >/dev/null 2>&1; then
    echo "test-hooks: $KEY exists (another run?)" >&2
    exit 2
fi
T="$(mktemp -d "${TMPDIR:-/tmp}/win-app-hooks.XXXXXX")"
trap 'wincmd reg.exe delete "$KEY" /f >/dev/null 2>&1 || true; rm -rf "$T"' EXIT

fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

# the stand-ins: each run is noted in standin.log beside it; two programs that differ (the
# installed one is told apart by its size and time)
cat >"$T/standin.cs" <<'CS'
using System; using System.IO;
static class StandIn {
    static void Main(string[] args) {
        File.AppendAllText(Path.Combine(AppDomain.CurrentDomain.BaseDirectory, "standin.log"),
            string.Join(" ", args) + Environment.NewLine);
    }
}
CS
# shellcheck disable=SC2016  # PowerShell's own $ variables; the paths go in through the environment
SRC="$(cygpath -w "$T/standin.cs")" OUT="$(cygpath -w "$T/old.exe")" powershell.exe -NoProfile -NonInteractive -Command \
    'Add-Type -TypeDefinition (Get-Content -Raw -LiteralPath $env:SRC) -OutputAssembly $env:OUT -OutputType WindowsApplication' >/dev/null
cp "$T/old.exe" "$T/knows.exe"
printf '\0another build\0' >>"$T/knows.exe"
# the shells' source: with the flags, and from before them (a comment naming them does not count)
mkdir -p "$T/src-knows" "$T/src-old"
cat >"$T/src-knows/agent.rs" <<'RS'
pub const START_FLAG: &str = "--server-start";
pub const STOP_FLAG: &str = "--server-stop";
pub const REMOVE_FLAG: &str = "--server-remove";
RS
cat >"$T/src-old/agent.rs" <<'RS'
// the installer's hooks will call "--server-start", "--server-stop" and "--server-remove"
pub const AGENT_FLAG: &str = "--server-agent";
RS

# the hooks around a stand-in install (PREINSTALL, the new Maktaba.exe and the registration,
# POSTINSTALL) and uninstall (PREUNINSTALL, then what the uninstaller removes), as the
# template inserts them
cat >"$T/harness.nsi" <<'NSI'
Unicode true
!include LogicLib.nsh
OutFile "${OUT}"
RequestExecutionLevel user
SilentInstall silent
InstallDir "$TEMP\maktaba-hooks-unused"
Var UpdateMode
!define MAINBINARYNAME "Maktaba"
!define MAINBINARYSRCPATH "${SRC}"
!define UNINSTKEY "Software\MaktabaHooksTest\Uninstall\Maktaba"
!include "${HOOKS}"
Section
  SetShellVarContext current
  StrCpy $UpdateMode ${UPDATE}
  !if "${PHASE}" == "install"
    !insertmacro NSIS_HOOK_PREINSTALL
    SetOutPath $INSTDIR
    File "/oname=Maktaba.exe" "${SRC}"
    WriteRegStr SHCTX "${UNINSTKEY}" "DisplayName" "Maktaba hooks test"
    !insertmacro NSIS_HOOK_POSTINSTALL
  !else
    !insertmacro NSIS_HOOK_PREUNINSTALL
    ${If} $UpdateMode <> 1
      Delete "$INSTDIR\Maktaba.exe"
      DeleteRegKey SHCTX "${UNINSTKEY}"
    ${EndIf}
  !endif
SectionEnd
NSI
for v in "install knows 0" "install old 0" "uninstall knows 0" "uninstall knows 1"; do
    read -r phase src update <<<"$v"
    "$MAKENSIS" -V1 "-DPHASE=$phase" "-DUPDATE=$update" "-DSRC=$(cygpath -w "$T/$src.exe")" \
        "-DMAKTABA_SHELL_SRC=$(cygpath -w "$T/src-$src")" "-DHOOKS=$(cygpath -w "$HOOKS")" "-DOUT=$(cygpath -w "$T/$phase-$src-$update.exe")" "$(cygpath -w "$T/harness.nsi")" >/dev/null ||
        { echo "test-hooks: makensis failed for $v" >&2; exit 1; }
done

INST="$T/inst"
mkdir -p "$INST"
# step NAME PROGRAM WANT_RUNS WANT_RECORD: run PROGRAM over $INST; the stand-ins must have been
# run with exactly WANT_RUNS ("a|b"), the record must be "present" or "absent"
step() {
    local name="$1" prog="$2" want="$3" want_record="$4" runs record
    rm -f "$INST/standin.log"
    wincmd "$T/$prog.exe" "/D=$(cygpath -w "$INST")"
    runs=""
    if [ -f "$INST/standin.log" ]; then runs="$(tr -d '\r' <"$INST/standin.log" | paste -sd'|' -)"; fi
    record="$(wincmd reg.exe query "$KEY\\Uninstall\\Maktaba" /v MaktabaServerFlags 2>/dev/null | tr -d '\r' | sed -n 's/.*REG_SZ *//p' || true)"
    if [ "$runs" != "$want" ]; then
        fail "$name: Maktaba.exe ran with '${runs}', want '${want}'"
    elif { [ "$want_record" = present ] && [ -z "$record" ]; } || { [ "$want_record" = absent ] && [ -n "$record" ]; }; then
        fail "$name: the record is '${record}', want it $want_record"
    else
        ok "$name: ${want:-nothing asked}${record:+ (record $record)}"
    fi
}

echo "installer hooks: $HOOKS"
step "a fresh install of a flag-taking build" install-knows-0 "--server-start" present
# the previous release's pipeline wheel, beside the rest of its payload: an update would keep
# it next to the new one (installs over the old files), and the server builds from exactly one
mkdir -p "$INST/server/pipeline"
printf 'old wheel\n' >"$INST/server/pipeline/maktaba_pipeline-0.0.1-py3-none-any.whl"
printf 'constraints\n' >"$INST/server/pipeline/constraints.txt"
step "a reinstall of the same build" install-knows-0 "--server-stop|--server-start" present
if [ -e "$INST/server/pipeline/maktaba_pipeline-0.0.1-py3-none-any.whl" ]; then
    fail "the previous release's pipeline wheel was left in server\\pipeline"
elif [ ! -f "$INST/server/pipeline/constraints.txt" ]; then
    fail "the install removed more of server\\pipeline than the old wheel"
else
    ok "the previous release's pipeline wheel removed before the install, the rest left"
fi
step "an update to a build from before the flags" install-old-0 "--server-stop" absent
step "the uninstall of that" uninstall-knows-0 "" absent
cp "$T/old.exe" "$INST/Maktaba.exe"
step "the client-only app there, then a flag-taking build" install-knows-0 "--server-start" present
cp "$T/old.exe" "$INST/Maktaba.exe"   # put over it later: the record is stale
step "a client-only Maktaba.exe over it, then a flag-taking build" install-knows-0 "--server-start" present
step "the uninstall an update runs" uninstall-knows-1 "" present
step "an uninstall" uninstall-knows-0 "--server-stop|--server-remove" absent

if [ "$fails" -ne 0 ]; then
    echo "$fails check(s) failed"
    exit 1
fi
echo "all checks passed"
