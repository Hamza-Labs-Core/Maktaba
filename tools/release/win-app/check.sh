#!/usr/bin/env bash
# Smoke-tests the Windows app's server payload (Epic 38, story 38.11) and, with --installer,
# the NSIS installer built around it. Runs on Windows x86_64 in Git Bash (CI: the win-app
# gate and desktop-release).
#
#   every program starts (--version), every PE file is x86_64 and loads nothing from outside
#   the payload but Windows, ffmpeg has NVENC, Postgres initdb + start on 127.0.0.1 + the
#   extensions Maktaba creates (postgres.exe running on the payload's Visual C++ runtime),
#   the payload's own migrations (maktaba-api migrate up from outside any checkout),
#   the bundled Python builds a venv offline, and running it writes nothing into the payload
#   (the uninstaller removes the files it installed: a new one would be left behind), the
#   pipeline wheel and constraints, payload.json, the launcher's app-bundle mode.
#
#   --installer: a silent install (/S /NS /D=, current user) into a temporary folder, with a
#   space in its path, that holds a client-only app (a stand-in Maktaba.exe that takes no
#   --server-* flag and never exits, as the real one opens its window, and its registration)
#   and a state (server.toml, keys\, data\); every check above on the installed payload; the
#   app's files and the launcher beside the real Maktaba.exe; the same installer again (a
#   reinstall of the same build); the same installer the way the app's updater runs it (/S
#   /UPDATE: over the old files, uninstalling nothing) over an older release's pipeline
#   wheel: server\pipeline must then hold one wheel, the one payload.json names (Epic 35
#   review round 2, B-H1); then a silent uninstall: the program is gone, the state is
#   untouched. It refuses to run where Maktaba is installed for this user or its sign-in task
#   exists: the installer would replace that install's registration and the uninstall hook
#   would remove the task. What the installer writes to the registry for the test install is
#   put back as it was.
#
#   The hooks (installer-hooks.nsh) run `Maktaba.exe --server-stop | --server-start |
#   --server-remove` (story 38.3) only on a Maktaba.exe that takes them. Every run must
#   finish without waiting: a hook still running after 20 s is ended and fails the check.
#   Whether this build's Maktaba.exe takes them is decided as the installer decides it, from
#   the shell's source in this tree (knows-server-flags.ps1), and checked against what the
#   installed Maktaba.exe does: run with --server-start it must exit by itself when the
#   source takes the flags, and keep running (its window) when it does not. Where it takes
#   none, no hook may run it at all and the installer keeps no record; where it does, the
#   record (MaktabaServerFlags in the uninstall key) must be there and the hooks must have run
#   it with exactly the flags each step calls for (install --server-start; reinstall and
#   update --server-stop, --server-start; uninstall --server-stop, --server-remove). Each run is
#   watched in a job object (run-watched.ps1), which tells of every process it starts.
#
# Usage: check.sh [--payload DIR] [--installer SETUP.exe]
#   --payload    the payload (default: build/win-app/server)
#   --installer  install this setup.exe (and uninstall it), and check the installed copy
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

PAYLOAD="$REPO_ROOT/build/win-app/server"
SETUP=""
while [ $# -gt 0 ]; do
    case "$1" in
        --payload)   [ $# -ge 2 ] || { echo "--payload needs a value" >&2; exit 2; }; PAYLOAD="$2"; shift 2 ;;
        --installer) [ $# -ge 2 ] || { echo "--installer needs a value" >&2; exit 2; }; SETUP="$2"; shift 2 ;;
        -h|--help)   sed -n '2,/^set -e/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
        *)           echo "unknown option '$1'" >&2; exit 2 ;;
    esac
done
case "$(uname -s)" in
    MINGW*|MSYS*) ;;
    *) echo "check: the Windows app is checked on Windows, in Git Bash" >&2; exit 2 ;;
esac

fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }
note() { printf '  note  %s\n' "$*"; }
# run NAME CMD...: CMD exits 0 within 60 s; its first output line is shown
run() {
    local name="$1" out rc=0; shift
    out="$(timeout 60 "$@" 2>&1)" || rc=$?
    out="${out//$'\r'/}"
    if [ "$rc" -eq 0 ]; then ok "$name: $(printf '%s\n' "$out" | head -1)"; else fail "$name (exit $rc): $(printf '%s\n' "$out" | tail -3)"; fi
}
# win PATH...: the Windows form (long names) of each path, for the native programs
win() { cygpath -w -l "$@"; }
# wincmd CMD...: a Windows program whose /switches must not be taken for paths
wincmd() { MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*' "$@"; }
# reg_value KEY [NAME]: a registry value (the default one without NAME), empty when absent
reg_value() {
    local out
    if [ $# -ge 2 ]; then out="$(wincmd reg.exe query "$1" /v "$2" 2>/dev/null)" || return 0
    else out="$(wincmd reg.exe query "$1" /ve 2>/dev/null)" || return 0; fi
    printf '%s\n' "$out" | tr -d '\r' | sed -n 's/.*REG_[A-Z_]*[[:space:]]*//p' | head -1
}
reg_exists() { wincmd reg.exe query "$1" >/dev/null 2>&1; }

SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/win-app-check.XXXXXX")"
PGDATA_DIR="$SCRATCH/pgdata"
PG_CTL=""
REGISTRY_TOUCHED=0
cleanup() {
    # a check that failed half way leaves no server behind, and the registry as it was
    if [ -n "$PG_CTL" ] && [ -f "$PGDATA_DIR/postmaster.pid" ]; then
        "$PG_CTL" -D "$(win "$PGDATA_DIR")" -m immediate -w stop >/dev/null 2>&1 || true
    fi
    if [ "$REGISTRY_TOUCHED" = 1 ]; then
        # nothing of the test install keeps running (a hook's window would hold the uninstaller)
        # shellcheck disable=SC2016  # PowerShell's own $ variables; the folder goes in through the environment
        PREFIX="$(win "$INST")\\" powershell.exe -NoProfile -NonInteractive -Command \
            'Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith($env:PREFIX, [StringComparison]::OrdinalIgnoreCase) } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }' \
            >/dev/null 2>&1 || true
        restore_registry
    fi
    rm -rf "$SCRATCH" 2>/dev/null || true
}
trap cleanup EXIT

# --- the installer, part 1: install ----------------------------------------------------------
UNINSTALL_KEY='HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\Maktaba'
DEEPLINK_KEY='HKCU\Software\Classes\maktaba'
if [ -n "$SETUP" ]; then
    [ -f "$SETUP" ] || { echo "check: no installer at $SETUP" >&2; exit 2; }
    SETUP="$(cd "$(dirname "$SETUP")" && pwd)/$(basename "$SETUP")"
    if reg_exists "$UNINSTALL_KEY"; then
        echo "check: Maktaba is installed for this user ($(reg_value "$UNINSTALL_KEY" InstallLocation)):" \
            "a test install would replace its registration. Run --installer as another user or on another machine." >&2
        exit 2
    fi
    # the sign-in task: "Maktaba Server <this user's SID>" (story 38.3), "Maktaba Server" before
    sid="$(powershell.exe -NoProfile -NonInteractive -Command '[Security.Principal.WindowsIdentity]::GetCurrent().User.Value' | tr -d '\r')"
    for task in "Maktaba Server" "Maktaba Server $sid"; do
        if wincmd schtasks.exe /Query /TN "$task" >/dev/null 2>&1; then
            echo "check: the sign-in task '$task' exists: the test's hooks would start or remove it." >&2
            exit 2
        fi
    done
    # what the installer decides: does this tree's shell take the --server-* flags?
    TREE_HOOKS=0
    rc=0
    powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass \
        -File "$(win "$REPO_ROOT/apps/desktop/src-tauri/windows/knows-server-flags.ps1")" \
        -Source "$(win "$REPO_ROOT/apps/desktop/src-tauri/src")" || rc=$?
    case "$rc" in
        0) TREE_HOOKS=1 ;;
        1) ;;
        *) echo "check: cannot tell whether this tree's shell takes the --server-* flags (exit $rc)" >&2; exit 2 ;;
    esac
    if reg_exists "$DEEPLINK_KEY"; then
        echo "check: maktaba:// links are registered ($DEEPLINK_KEY): a test install would replace that." >&2
        exit 2
    fi
    # The installer remembers the install folder for the next one in HKCU\Software\<publisher>\
    # Maktaba (tauri: the publisher, else the identifier's second part); put back afterwards.
    conf="$REPO_ROOT/apps/desktop/src-tauri/tauri.conf.json"
    manufacturer="$(sed -n 's/^ *"publisher": *"\([^"]*\)".*/\1/p' "$conf" | head -1)"
    [ -n "$manufacturer" ] ||
        manufacturer="$(sed -n 's/^ *"identifier": *"\([^"]*\)".*/\1/p' "$conf" | head -1 | cut -d. -f2)"
    [ -n "$manufacturer" ] || { echo "check: no publisher or identifier in $conf" >&2; exit 2; }
    MANU_KEY="HKCU\\Software\\$manufacturer"
    PRODUCT_KEY="$MANU_KEY\\Maktaba"
    manu_had=0 product_had=0
    if reg_exists "$MANU_KEY"; then manu_had=1; fi
    if reg_exists "$PRODUCT_KEY"; then
        product_had=1
        wincmd reg.exe export "$PRODUCT_KEY" "$(win "$SCRATCH/product.reg")" /y >/dev/null
    fi
    # A shell that opens its window for a hook (before 38.3) makes its app data folders,
    # empty (WebView2's data goes to the scratch folder, below): removed when it did.
    bundle_id="$(sed -n 's/^ *"identifier": *"\([^"]*\)".*/\1/p' "$conf" | head -1)"
    app_dirs=()
    for d in "$(cygpath -u "${LOCALAPPDATA:-}")/$bundle_id" "$(cygpath -u "${APPDATA:-}")/$bundle_id"; do
        [ -e "$d" ] || app_dirs+=("$d")
    done
    # restore_registry: what the test install wrote for this user, removed or put back (and
    # the empty app data folders above)
    restore_registry() {
        REGISTRY_TOUCHED=0
        for d in ${app_dirs[@]+"${app_dirs[@]}"}; do
            if [ -d "$d" ]; then rmdir "$d" 2>/dev/null || note "left $(win "$d") (not empty)"; fi
        done
        if reg_exists "$UNINSTALL_KEY"; then wincmd reg.exe delete "$UNINSTALL_KEY" /f >/dev/null 2>&1 || true; fi
        # the maktaba:// registration (the guard: there was none before)
        if reg_exists "$DEEPLINK_KEY"; then wincmd reg.exe delete "$DEEPLINK_KEY" /f >/dev/null 2>&1 || true; fi
        if [ "$manu_had" = 0 ]; then
            if reg_exists "$MANU_KEY"; then wincmd reg.exe delete "$MANU_KEY" /f >/dev/null 2>&1 || true; fi
        else
            if reg_exists "$PRODUCT_KEY"; then wincmd reg.exe delete "$PRODUCT_KEY" /f >/dev/null 2>&1 || true; fi
            if [ "$product_had" = 1 ]; then wincmd reg.exe import "$(win "$SCRATCH/product.reg")" >/dev/null 2>&1 || true; fi
        fi
    }

    INST="$SCRATCH/install dir/Maktaba"
    mkdir -p "$INST/keys" "$INST/data/supervisor" "$SCRATCH/localappdata" "$SCRATCH/webview2"
    printf '[server]\n# a state the install and the uninstall leave alone\n' >"$INST/server.toml"
    printf 'not a real key\n' >"$INST/keys/secrets.key"
    printf 'state\n' >"$INST/data/supervisor/state.txt"
    state_sums() { (cd "$INST" && sha256sum server.toml keys/secrets.key data/supervisor/state.txt 2>&1); }
    STATE_BEFORE="$(state_sums)"

    # Runs the installer, the uninstaller or Maktaba.exe in a job object and reports all it
    # started (run-watched.ps1): the uninstaller's copy of itself in %TEMP%, which removes the
    # registration well after uninstall.exe is gone, and each hook's run of Maktaba.exe, read
    # the moment it starts. What runs from the install folder after the grace is ended.
    # watched EXE ARGS [GRACE]: runs it as above (GRACE seconds, default 20, before what runs
    # from the install folder is ended); sets WATCHED_EXIT (its exit code), WATCHED_CHILDREN
    # ("<name> exit <code>" of the other programs it started itself), WATCHED_RAN (the command
    # line of each of the app's) and WATCHED_ENDED (what had to be ended), one per line
    watched() {
        local out line
        WATCHED_EXIT=none WATCHED_ENDED="" WATCHED_CHILDREN="" WATCHED_RAN=""
        # the hooks' app (story 38.3) keeps its state in %LOCALAPPDATA%\Maktaba: a scratch one
        out="$(LOCALAPPDATA="$(win "$SCRATCH/localappdata")" WEBVIEW2_USER_DATA_FOLDER="$(win "$SCRATCH/webview2")" \
            wincmd powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass \
            -File "$(win "$SCRIPT_DIR/run-watched.ps1")" -Exe "$(win "$1")" -Arguments "$2" -Dir "$(win "$INST")" \
            -Grace "${3:-20}" 2>&1)" || true
        while IFS=$'\r' read -r line; do
            case "$line" in
                "exit "*)  WATCHED_EXIT="${line#exit }" ;;
                "child "*) WATCHED_CHILDREN+="${line#child }"$'\n' ;;
                "ended "*) WATCHED_ENDED+="${line#ended }"$'\n' ;;
                "ran "*)   WATCHED_RAN+="${line#ran }"$'\n' ;;
                "missed "*|"timeout "*) fail "$(basename "$1") $2: the watcher: $line" ;;
                "") ;;
                *) printf '        %s\n' "$line" ;;
            esac
        done <<<"$out"
    }
    # hooks WHAT FLAG...: what the hooks of the last run did, judged by whether this tree's
    # shell takes the flags (the header): none may still run after 20 s; with the flags,
    # Maktaba.exe must have run with exactly FLAG..., in that order (a run whose command line
    # could not be read, "?", stands for any flag); without them, it must not have run at all
    hooks() {
        local n ended=0 what="$1" got want
        shift
        while IFS= read -r n; do
            if [ -n "$n" ]; then fail "$what: a hook did not exit within 20 s, ended: $n"; ended=1; fi
        done <<<"$WATCHED_ENDED"
        # the flag of each run of Maktaba.exe (its last word), in order
        got="$(printf '%s' "$WATCHED_RAN" | { grep -i 'Maktaba\.exe' || true; } | sed 's/.* //' | paste -sd' ' -)"
        if [ "$SHELL_HOOKS" = 0 ]; then
            if [ -n "$WATCHED_RAN" ]; then
                fail "$what: a hook ran Maktaba.exe, which takes no --server-* flag: $(printf '%s' "$WATCHED_RAN" | paste -sd'|' -)"
            else
                ok "$what: no hook ran Maktaba.exe (it takes no --server-* flag)"
            fi
            return
        fi
        want="$*"
        if [ "$(printf '%s' "$got" | sed 's/[^ ][^ ]*/x/g')" != "$(printf '%s' "$want" | sed 's/[^ ][^ ]*/x/g')" ] ||
            ! printf '%s\n' "$got" | grep -qxE "$(printf '%s' "$want" | sed 's/[^ ][^ ]*/(&|[?])/g')"; then
            fail "$what: the hooks ran Maktaba.exe with '${got}', want '${want}'"
        elif [ "$ended" = 0 ]; then
            ok "$what: the hooks ran Maktaba.exe ${got// / then }, each exited by itself"
        fi
    }
    # flags_record: the installer's record that the installed Maktaba.exe takes the flags
    flags_record() { reg_value "$UNINSTALL_KEY" MaktabaServerFlags; }
    record_check() {
        local rec
        rec="$(flags_record)"
        if [ "$SHELL_HOOKS" = 1 ]; then
            if [ -n "$rec" ] && [ "$rec" != "$STALE_RECORD" ]; then
                ok "$1: the record that Maktaba.exe takes the flags ($rec)"
            else
                fail "$1: this tree's shell takes the flags, but the installer recorded nothing ('$rec'): no hook would ever stop the server"
            fi
        elif [ -z "$rec" ]; then
            ok "$1: no record that Maktaba.exe takes the flags (the client-only app's stale one dropped)"
        else
            fail "$1: a record that Maktaba.exe takes the flags, which this tree's shell does not: $rec"
        fi
    }

    # A client-only app installed there before: Maktaba.exe, registered as the NSIS client
    # installer does (an older version), with a record left by an earlier server install
    # whose Maktaba.exe this is not. The stand-in takes no flag and, like the real app's
    # window, does not exit; it notes every start in standin.log.
    cat >"$SCRATCH/standin.cs" <<'CS'
using System; using System.IO; using System.Threading;
static class StandIn {
    static void Main(string[] args) {
        File.AppendAllText(Path.Combine(AppDomain.CurrentDomain.BaseDirectory, "standin.log"),
            string.Join(" ", args) + Environment.NewLine);
        Thread.Sleep(TimeSpan.FromMinutes(10));
    }
}
CS
    # shellcheck disable=SC2016  # PowerShell's own $ variables; the paths go in through the environment
    SRC="$(win "$SCRATCH/standin.cs")" OUT="$(win "$INST")\\Maktaba.exe" powershell.exe -NoProfile -NonInteractive -Command \
        'Add-Type -TypeDefinition (Get-Content -Raw -LiteralPath $env:SRC) -OutputAssembly $env:OUT -OutputType WindowsApplication' >/dev/null 2>&1 ||
        { echo "check: cannot build the stand-in Maktaba.exe (PowerShell's Add-Type)" >&2; exit 2; }
    REGISTRY_TOUCHED=1
    STALE_RECORD="1 2.3"
    wincmd reg.exe add "$UNINSTALL_KEY" /v DisplayName /d Maktaba /f >/dev/null
    wincmd reg.exe add "$UNINSTALL_KEY" /v DisplayVersion /d 0.0.1 /f >/dev/null
    wincmd reg.exe add "$UNINSTALL_KEY" /v InstallLocation /d "\"$(win "$INST")\"" /f >/dev/null
    wincmd reg.exe add "$UNINSTALL_KEY" /v MaktabaServerFlags /d "$STALE_RECORD" /f >/dev/null

    echo "installer: $SETUP (over a client-only app)"
    watched "$SETUP" "/S /NS /D=$(win "$INST")"
    if [ "$WATCHED_EXIT" = 0 ] && [ -f "$INST/Maktaba.exe" ] && [ -f "$INST/server/payload.json" ]; then
        ok "silent install into $(win "$INST")"
    else
        fail "silent install (exit $WATCHED_EXIT)"
    fi
    if [ -f "$INST/standin.log" ]; then
        fail "the client-only Maktaba.exe was run by a hook: $(tr -d '\r' <"$INST/standin.log" | paste -sd'|' -)"
    else
        ok "the client-only Maktaba.exe that was there was not run (it takes no --server-* flag)"
    fi
    SHELL_HOOKS="$TREE_HOOKS"
    if [ "$SHELL_HOOKS" = 1 ]; then
        echo "  (this tree's shell takes the --server-* flags)"
    else
        echo "  (this tree's shell takes no --server-* flag: story 38.3 is not in it)"
    fi
    hooks "install" --server-start
    record_check "install"
    # The decision against the program: with --server-start (no sign-in task exists, so it has
    # nothing to start) a Maktaba.exe that takes the flags exits by itself; one before them
    # opens its window and is ended after 10 s.
    watched "$INST/Maktaba.exe" "--server-start" 10
    if [ -z "$WATCHED_ENDED" ] && [ "$WATCHED_EXIT" != none ]; then
        probe="exited by itself (exit $WATCHED_EXIT)"
        took=1
    else
        probe="did not exit (ended after 10 s)"
        took=0
    fi
    if [ "$took" = "$SHELL_HOOKS" ]; then
        ok "Maktaba.exe --server-start $probe, as the shell's source says"
    else
        fail "Maktaba.exe --server-start $probe, but the shell's source says it takes $([ "$SHELL_HOOKS" = 1 ] && echo the || echo no) --server-* flags"
    fi
    publisher="$(reg_value "$UNINSTALL_KEY" Publisher)"
    if [ -n "$publisher" ] && [ "$publisher" != "$manufacturer" ]; then
        fail "the installer's publisher is '$publisher', check.sh expected '$manufacturer' (it puts HKCU\\Software\\<publisher>\\Maktaba back)"
    fi
    PAYLOAD="$INST/server"
fi

if [ ! -f "$PAYLOAD/payload.json" ]; then
    fail "no payload at $PAYLOAD"
else
PAYLOAD="$(cd "$PAYLOAD" && pwd)"
PY="$PAYLOAD/python/python.exe"
tool() { "$PY" -I "$(win "$SCRIPT_DIR/payload_tool.py")" "$@"; }

touch "$SCRATCH/before"
sleep 1   # mtime granularity: anything the checks write into the payload is newer

echo "payload: $(win "$PAYLOAD")"
echo "programs"
for b in maktaba-server maktaba-api maktaba-streaming maktaba-launcher; do
    run "$b" "$PAYLOAD/bin/$b.exe" --version
done
run "maktaba-cloudlink" "$PAYLOAD/bin/maktaba-cloudlink.exe" version
for b in postgres initdb pg_ctl pg_dump pg_dumpall pg_restore psql pg_isready pg_upgrade pg_controldata pg_resetwal pg_basebackup; do
    run "$b" "$PAYLOAD/postgres/bin/$b.exe" --version
done
run ffmpeg "$PAYLOAD/ffmpeg/ffmpeg.exe" -hide_banner -version
run ffprobe "$PAYLOAD/ffmpeg/ffprobe.exe" -hide_banner -version
run python "$PY" -I -c 'import sys, ssl, sqlite3, venv, ensurepip, ctypes; print(sys.version.split()[0], ssl.OPENSSL_VERSION)'

echo "PE files"
pe_out="$(tool check "$(win "$PAYLOAD")" 2>&1)" && pe_rc=0 || pe_rc=$?
pe_out="${pe_out//$'\r'/}"
if [ "$pe_rc" -eq 0 ]; then
    ok "$(printf '%s\n' "$pe_out" | tail -1), all x86_64, nothing loaded from outside the payload but Windows"
else
    while IFS= read -r line; do fail "$line"; done < <(printf '%s\n' "$pe_out" | sed '$d' | head -20)
fi

echo "ffmpeg"
encoders="$("$PAYLOAD/ffmpeg/ffmpeg.exe" -hide_banner -encoders 2>/dev/null || true)"
if grep -qw h264_nvenc <<<"$encoders"; then ok "h264_nvenc (the TV's encoder on Windows)"; else fail "ffmpeg has no h264_nvenc"; fi
hwaccels="$("$PAYLOAD/ffmpeg/ffmpeg.exe" -hide_banner -hwaccels 2>/dev/null || true)"
if grep -qw cuda <<<"$hwaccels"; then ok "-hwaccel cuda"; else fail "ffmpeg has no cuda hwaccel"; fi

echo "PostgreSQL"
# the only logical decoding output plugin is pgoutput: another one (test_decoding) would let a
# machine's replication role decode every table, past what is published to it (Epic 37)
plugins="$(find "$PAYLOAD/postgres/lib" -type f ! -name 'pgoutput.*' -exec grep -l -a -F _PG_output_plugin_init {} + 2>/dev/null || true)"
if [ -z "$plugins" ]; then ok "no output plugin but pgoutput"; else fail "output plugins besides pgoutput: $(printf '%s\n' "$plugins" | tr '\n' ' ')"; fi
pg="$PAYLOAD/postgres/bin"
PG_CTL="$pg/pg_ctl.exe"
# a free port on 127.0.0.1, from the system
port="$("$PY" -I -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()' | tr -d '\r')"
# the supervisor's initdb (pgbundle): UTF8, the C locale
if "$pg/initdb.exe" -D "$(win "$PGDATA_DIR")" -U maktaba --auth=trust -E UTF8 --locale=C >"$SCRATCH/initdb.log" 2>&1 &&
    "$PG_CTL" -D "$(win "$PGDATA_DIR")" -l "$(win "$SCRATCH/pg.log")" -w -t 60 \
        -o "-p $port -c listen_addresses=127.0.0.1" start >"$SCRATCH/pg_ctl.log" 2>&1; then
    ok "initdb, started on 127.0.0.1:$port"
    run "pg_isready" "$pg/pg_isready.exe" -h 127.0.0.1 -p "$port"
    want="pg_trgm, pgcrypto, unaccent: true"
    got="$(timeout 60 "$pg/psql.exe" -h 127.0.0.1 -p "$port" -U maktaba -d postgres -Atq -v ON_ERROR_STOP=1 -c \
        "CREATE EXTENSION pg_trgm; CREATE EXTENSION pgcrypto; CREATE EXTENSION unaccent;
         SELECT 'pg_trgm, pgcrypto, unaccent: ' || (similarity('maktaba', 'maktabah') > 0.5
             AND encode(digest('maktaba', 'sha256'), 'hex') = '$(printf maktaba | sha256sum | awk '{print $1}')'
             AND unaccent(U&'H\00F4tel') = 'Hotel')::text;" 2>&1 | tr -d '\r' || true)"
    if [ "$got" = "$want" ]; then ok "$got"; else fail "extensions: $got"; fi
    run "pg_dump" "$pg/pg_dump.exe" -h 127.0.0.1 -p "$port" -U maktaba -Fc -f "$(win "$SCRATCH/dump")" postgres
    # the schema from the payload alone: maktaba-api finds server\migrations beside its bin\,
    # run from a folder that is no checkout and without MAKTABA_MIGRATIONS_DIR
    if [ -d "$PAYLOAD/migrations" ] && diff -rq "$PAYLOAD/migrations" "$REPO_ROOT/shared/db/migrations" >/dev/null; then
        ok "migrations\\ ($(find "$PAYLOAD/migrations" -type f | wc -l | tr -d ' ') files) = shared/db/migrations"
    else
        fail "the payload's migrations\\ is missing or differs from shared/db/migrations"
    fi
    run "createdb" "$pg/psql.exe" -h 127.0.0.1 -p "$port" -U maktaba -d postgres -Atq -c "CREATE DATABASE maktaba_check;"
    if grep -q 'func migrationsDirFor' "$REPO_ROOT/api/migrate.go"; then
        run "migrations (maktaba-api migrate up, outside any checkout)" \
            env -C "$SCRATCH" -u MAKTABA_MIGRATIONS_DIR \
            DATABASE_URL="postgres://maktaba@127.0.0.1:$port/maktaba_check?sslmode=disable" \
            "$PAYLOAD/bin/maktaba-api.exe" migrate up
    else
        # this tree's maktaba-api looks only for shared/db/migrations above its working folder
        note "this maktaba-api predates the payload's migrations\\ lookup (#184): migrate up is not checked"
    fi
    # postgres.exe loads the Visual C++ runtime from beside it, not from System32
    pid="$(head -1 "$PGDATA_DIR/postmaster.pid" | tr -d '\r')"
    # shellcheck disable=SC2016  # PowerShell's own $ variables; the pid goes in through the environment
    mods="$(PG_PID="$pid" powershell.exe -NoProfile -NonInteractive -Command \
        '(Get-Process -Id $env:PG_PID).Modules | Where-Object { $_.ModuleName -match "^(vcruntime140|msvcp140)" } | ForEach-Object { $_.FileName }' 2>&1 | tr -d '\r' || true)"
    # Each DLL's folder must be postgres\bin itself, both in their long form: Windows reports
    # the path a program was started from, which may hold 8.3 names (TEMP is C:\Users\
    # RUNNER~1\... on GitHub's runners), and Windows paths compare without case.
    pgwin="$(win "$pg")"
    outside="" names=""
    while IFS= read -r m; do
        [ -n "$m" ] || continue
        m="$(win "$m" 2>/dev/null || printf '%s' "$m")"
        dir="${m%\\*}"
        names+="${m##*\\} "
        if [ "${dir,,}" != "${pgwin,,}" ]; then outside+="$m "; fi
    done <<<"$mods"
    if ! grep -qi 'vcruntime140\.dll' <<<"$names"; then
        fail "postgres.exe (pid $pid): could not list its Visual C++ runtime DLLs: $mods"
    elif [ -n "$outside" ]; then
        fail "postgres.exe loads the Visual C++ runtime from outside the payload's postgres\\bin ($pgwin): $outside"
    else
        ok "postgres.exe runs on the payload's Visual C++ runtime (${names% })"
    fi
    if "$PG_CTL" -D "$(win "$PGDATA_DIR")" -m fast -w -t 60 stop >/dev/null 2>&1; then ok "pg_ctl stop -m fast"; else fail "pg_ctl stop -m fast"; fi
else
    fail "initdb/start: $(tail -3 "$SCRATCH/initdb.log" "$SCRATCH/pg.log" "$SCRATCH/pg_ctl.log" 2>/dev/null | tr -d '\r')"
fi

echo "Python"
run "stdlib imports" "$PY" -I -c 'import asyncio, bz2, ctypes, decimal, email.message, hashlib, http.client, json, lzma, multiprocessing, sqlite3, ssl, uuid, venv, xml.etree.ElementTree, zlib, zoneinfo; print("ok")'
run "venv (offline, with pip)" "$PY" -I -m venv "$(win "$SCRATCH/venv")"
if [ -f "$SCRATCH/venv/Scripts/pip.exe" ]; then ok "venv has pip"; else fail "venv has no pip"; fi
run "venv's python" "$SCRATCH/venv/Scripts/python.exe" -I -c 'import sys; print("in a venv:", sys.prefix != sys.base_prefix)'

echo "pipeline"
whl=("$PAYLOAD"/pipeline/maktaba_pipeline-*-py3-none-any.whl)
if [ -f "${whl[0]}" ]; then ok "wheel ${whl[0]##*/}"; else fail "no pipeline wheel"; fi
if [ -s "$PAYLOAD/pipeline/constraints.txt" ] && grep -q -- '--hash=sha256:' "$PAYLOAD/pipeline/constraints.txt"; then
    ok "constraints.txt (hashed)"
else
    fail "constraints.txt missing or without hashes"
fi
# the default set is PyPI's (torch's CPU build); the cuda set has torch's CUDA build and names
# PyTorch's index (story 38.9)
if grep -q -- '--extra-index-url' "$PAYLOAD/pipeline/constraints.txt"; then
    fail "constraints.txt names an index beside PyPI (the cuda set's)"
fi
if [ -s "$PAYLOAD/pipeline/constraints-cuda.txt" ] && grep -q -- '--hash=sha256:' "$PAYLOAD/pipeline/constraints-cuda.txt" \
    && grep -qE '^torch==[^ ;]*\+cu' "$PAYLOAD/pipeline/constraints-cuda.txt"; then
    ok "constraints-cuda.txt (hashed, torch's CUDA build)"
else
    fail "constraints-cuda.txt missing, without hashes or without torch's CUDA build"
fi
for heavy in torch nvidia ctranslate2 mlx; do
    if find "$PAYLOAD" -iname "${heavy}*" -print -quit | grep -q .; then fail "$heavy is in the payload (it downloads on first run)"; fi
done
run "payload.json" "$PY" -I - "$(win "$PAYLOAD")" "$("$pg/postgres.exe" --version 2>&1 | tr -d '\r')" <<'PY'
import hashlib, json, sys
from pathlib import Path
root = Path(sys.argv[1])
d = json.loads((root / "payload.json").read_text(encoding="utf-8"))
assert d["schema"] == 1, d["schema"]
assert d["platform"] == "windows-x86_64", d["platform"]
assert d["version"], "no version"
for k in ("postgres", "ffmpeg", "python"):
    assert d[k]["version"] and d[k]["source"].startswith("https://") and len(d[k]["sha256"]) == 64, k
pg = d["postgres"]["version"].removesuffix(".0")
assert sys.argv[2].endswith(" " + pg), f"postgres.exe says {sys.argv[2]!r}, payload.json {pg}"
assert sys.version.split()[0] == d["python"]["version"], (sys.version, d["python"]["version"])
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
assert sha(root / "pipeline" / d["pipeline"]["wheel"]) == d["pipeline"]["sha256"], "the wheel's sha256"
assert sha(root / "pipeline" / "constraints.txt") == d["pipeline"]["constraints_sha256"], "constraints.txt's sha256"
assert sha(root / "pipeline" / "constraints-cuda.txt") == d["pipeline"]["constraints_cuda_sha256"], "constraints-cuda.txt's sha256"
for lib in d["postgres"]["libraries"]:
    assert (root / "postgres" / "bin" / lib).is_file(), f"postgres/bin/{lib}"
assert d["msvc_runtime"]["version"], "msvc_runtime"
print("version", d["version"], "for", d["platform"])
PY

echo "launcher"
launcher="$PAYLOAD/bin/maktaba-launcher.exe"
status="$("$launcher" --app-bundle "$(win "$PAYLOAD")" --root "$(win "$SCRATCH/state")" --status 2>&1 | tr -d '\r' || true)"
if grep -q 'updates: *with the app' <<<"$status"; then
    ok "--app-bundle (what the agent passes): updates come with the app"
else
    fail "launcher --app-bundle --status: $status"
fi
# Story 38.2 teaches the launcher the Windows app's layout (<install>\server\bin beside
# <install>\Maktaba.exe, with payload.json); before it, only Maktaba.app is recognised.
help="$("$launcher" --help 2>&1 | tr -d '\r' || true)"
if [ -n "$SETUP" ]; then
    app_root="$INST"
else
    # a stand-in install: the launcher looks only for the files
    app_root="$SCRATCH/app"
    mkdir -p "$app_root/server/bin"
    : >"$app_root/Maktaba.exe"
    cp "$PAYLOAD/payload.json" "$app_root/server/"
    cp "$launcher" "$app_root/server/bin/"
fi
status="$("$app_root/server/bin/maktaba-launcher.exe" --root "$(win "$SCRATCH/state")" --status 2>&1 | tr -d '\r' || true)"
if grep -q 'updates: *with the app' <<<"$status"; then
    ok "launcher in <install>\\server\\bin beside Maktaba.exe: app-bundle mode, detected"
elif grep -q 'Maktaba.exe' <<<"$help"; then
    fail "the launcher did not detect the app's layout: $status"
else
    note "this launcher predates story 38.2 (no Windows app layout): its detection is not checked"
fi

echo "payload untouched by the checks"
written="$(find "$PAYLOAD" -newer "$SCRATCH/before" | sed -n '1,5p')"
if [ -z "$written" ]; then ok "nothing written into the payload"; else fail "the checks wrote into the payload: $written"; fi
fi

# --- the installer, part 2: the app, then uninstall ---------------------------------------------
if [ -n "$SETUP" ]; then
    echo "the installed app: $(win "$INST")"
    for f in Maktaba.exe uninstall.exe server/bin/maktaba-server.exe server/payload.json; do
        if [ -f "$INST/$f" ]; then ok "$f"; else fail "no $f"; fi
    done
    built="$REPO_ROOT/build/win-app/server/payload.json"
    if [ -f "$built" ] && [ -f "$INST/server/payload.json" ]; then
        if cmp -s "$built" "$INST/server/payload.json"; then ok "server\\payload.json is the one payload.sh built"; else note "server\\payload.json differs from ${built#"$REPO_ROOT"/} (an installer from another build)"; fi
    fi
    loc="$(reg_value "$UNINSTALL_KEY" InstallLocation)"
    if [ "${loc//\"/}" = "$(win "$INST")" ]; then ok "registered for this user (HKCU), at the test folder"; else fail "uninstall registration: InstallLocation '$loc'"; fi
    if [ "$(state_sums)" = "$STATE_BEFORE" ]; then ok "the state beside it is untouched by the install"; else fail "the install changed the state: $(state_sums)"; fi

    # the same build again: its hooks find the Maktaba.exe they installed (and its record)
    echo "reinstall (the same installer over it)"
    watched "$SETUP" "/S /NS /D=$(win "$INST")"
    if [ "$WATCHED_EXIT" = 0 ] && [ -f "$INST/Maktaba.exe" ] && [ -f "$INST/server/payload.json" ]; then
        ok "silent reinstall"
    else
        fail "silent reinstall (exit $WATCHED_EXIT)"
    fi
    hooks "reinstall" --server-stop --server-start
    record_check "reinstall"
    if [ "$(state_sums)" = "$STATE_BEFORE" ]; then ok "the state is untouched by the reinstall"; else fail "the reinstall changed the state: $(state_sums)"; fi

    # An update as the app's updater runs it (tauri-plugin-updater: the installer with
    # /UPDATE, which installs over the old files and uninstalls nothing) from an older
    # release, whose pipeline wheel (its name carries its version) is still there. The server
    # builds the pipeline's venv from one wheel; two would roll every update back.
    echo "update (/UPDATE, over an older release's pipeline wheel)"
    printf 'an older release wheel\n' >"$INST/server/pipeline/maktaba_pipeline-0.0.0a1-py3-none-any.whl"
    watched "$SETUP" "/S /UPDATE /NS /D=$(win "$INST")"
    if [ "$WATCHED_EXIT" = 0 ] && [ -f "$INST/Maktaba.exe" ] && [ -f "$INST/server/payload.json" ]; then
        ok "silent update (/UPDATE)"
    else
        fail "silent update /UPDATE (exit $WATCHED_EXIT)"
    fi
    hooks "update" --server-stop --server-start
    record_check "update"
    named="$("$INST/server/python/python.exe" -I -c 'import json, sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["pipeline"]["wheel"])' \
        "$(win "$INST/server/payload.json")" 2>&1 | tr -d '\r' || true)"
    wheels=()
    for w in "$INST"/server/pipeline/maktaba_pipeline-*.whl; do
        if [ -f "$w" ]; then wheels+=("${w##*/}"); fi
    done
    if [ "${#wheels[@]}" -eq 1 ] && [ -n "$named" ] && [ "${wheels[0]}" = "$named" ]; then
        ok "server\\pipeline holds one wheel, the one payload.json names ($named)"
    else
        fail "server\\pipeline after the update: ${wheels[*]:-no wheel} (payload.json names '$named')"
    fi
    if [ "$(state_sums)" = "$STATE_BEFORE" ]; then ok "the state is untouched by the update"; else fail "the update changed the state: $(state_sums)"; fi

    echo "uninstall"
    watched "$INST/uninstall.exe" "/S"
    hooks "uninstall" --server-stop --server-remove
    # NSIS's uninstall.exe runs the uninstall from a copy of itself in %TEMP% (Un.exe) and
    # exits at once: the checks below mean something only once that copy has finished, and
    # its exit code is the uninstall's
    copy="$(printf '%s' "$WATCHED_CHILDREN" | grep -E '^(Un|Un_A|Au_) exit ' | head -1 || true)"
    if [ "$WATCHED_EXIT" != 0 ]; then
        fail "uninstall.exe /S exited $WATCHED_EXIT"
    elif [ -z "$copy" ]; then
        fail "uninstall.exe /S: the copy it runs the uninstall from was not seen (it started: ${WATCHED_CHILDREN:-nothing})"
    elif [ "${copy##* exit }" != 0 ]; then
        fail "uninstall.exe /S: its copy failed ($copy)"
    else
        ok "uninstall.exe /S and the copy it ran from %TEMP% finished ($copy)"
    fi
    left=0
    for f in Maktaba.exe uninstall.exe server; do
        if [ -e "$INST/$f" ]; then
            fail "left behind: $f $(find "$INST/$f" -type f 2>/dev/null | sed -n '1,3p' | tr '\n' ' ')"
            left=1
        fi
    done
    [ "$left" = 1 ] || ok "Maktaba.exe, uninstall.exe and server\\ removed"
    if [ "$(state_sums)" = "$STATE_BEFORE" ]; then ok "the state is still there, untouched (server.toml, keys\\, data\\)"; else fail "the uninstall changed the state: $(state_sums)"; fi
    if reg_exists "$UNINSTALL_KEY"; then fail "the uninstall registration is still there"; else ok "unregistered"; fi
    if reg_exists "$DEEPLINK_KEY"; then
        note "the uninstaller left $DEEPLINK_KEY ($(reg_value "$DEEPLINK_KEY\\shell\\open\\command"))"
    fi
    # the remembered install folder and anything left, as they were
    restore_registry
fi

if [ "$fails" -ne 0 ]; then
    echo "$fails check(s) failed"
    exit 1
fi
echo "all checks passed"
