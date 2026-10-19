#!/usr/bin/env bash
# Maktaba node installer for macOS and Linux (Epic 31, multi-instance).
#
# Makes this machine a worker of a Maktaba home: builds the pipeline's Python
# environment, joins the home when the machine has not joined yet (`maktaba-pipeline
# join` shows a pairing code that the admin types on the home), then installs the
# worker as a service that starts at boot or login and restarts when it fails:
#
#   macOS  LaunchAgent  ~/Library/LaunchAgents/com.maktaba.node[.<N>].plist
#   Linux  systemd user units maktaba-node.service + maktaba-node@<N>.service
#          (--system: system units that run as --run-as USER)
#
# Each worker runs the venv's `maktaba-pipeline run --node`: exit 78 (not joined yet)
# is retried every 30 s, exit 77 (the home removed this machine) leaves it stopped.
#
# Running it again updates in place: rebuilds the environment from --source (after a
# `git pull`), re-renders the units and restarts the workers. See README.md.
#
# Usage:
#   install-node.sh [options]              install, or update
#   install-node.sh --uninstall [--purge]  remove (uninstall-node.sh does the same)
#
# Options:
#   --home URL       the home's address, for example http://192.168.1.20:8080 (skips
#                    discovery; passed to join)
#   --name NAME      this machine's name on the home (passed to join)
#   --instances N    N extra transcription workers, maktaba-node@1..N, each with
#                    MAKTABA_STT_SLOT=stt-<i> and only the transcribe stage; the main
#                    worker then leaves transcribe to them. Default: keep the ones
#                    installed (none on a first install).
#   --extras LIST    pipeline extras, comma-separated, or "none" (default: stt on Apple
#                    Silicon, none elsewhere; remembered for updates)
#   --source DIR     the pipeline source (default: pipeline/ of the repo holding this
#                    script; remembered for updates)
#   --system         Linux: system units, started at boot with no login (needs root)
#   --run-as USER    with --system: the account the worker runs as (default: maktaba,
#                    created when missing; remembered for updates)
#   --no-join        do not join now; the service waits and starts once joined
#   --no-start       install and enable, but do not start or restart anything
#   --no-linger      Linux user units: leave lingering off (the worker then starts at
#                    login instead of at boot)
#   --dry-run        print the commands instead of running them; rendered files go
#                    under a temporary directory (printed at the end)
#   --uninstall      stop and remove the services and the node's Python environment
#   --purge          with --uninstall: also remove the logs and the node*.env settings
#   -h, --help       this text
#
# The node config (node.json: this machine's identity and the home's credentials) is
# never removed. Remove the machine on the home first, then delete that file by hand.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# --- options ---------------------------------------------------------------------
ACTION=install
HOME_URL=""
NODE_NAME=""
INSTANCES=""
EXTRAS=""
SOURCE=""
SCOPE=user
RUN_AS=""
DO_JOIN=1
DO_START=1
DO_LINGER=1
PURGE=0
DRY_RUN=0
DRY_ROOT=""

info()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33mwarn:\033[0m %s\n' "$*" >&2; }
die()   { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
    awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"
}

need_value() { [ $# -ge 2 ] && [ -n "$2" ] || die "$1 needs a value"; }

while [ $# -gt 0 ]; do
    case "$1" in
        --home)      need_value "$@"; HOME_URL="$2"; shift 2 ;;
        --name)      need_value "$@"; NODE_NAME="$2"; shift 2 ;;
        --instances) need_value "$@"; INSTANCES="$2"; shift 2 ;;
        --extras)    need_value "$@"; EXTRAS="$2"; shift 2 ;;
        --source)    need_value "$@"; SOURCE="$2"; shift 2 ;;
        --system)    SCOPE=system; shift ;;
        --run-as)    need_value "$@"; RUN_AS="$2"; shift 2 ;;
        --no-join)   DO_JOIN=0; shift ;;
        --no-start)  DO_START=0; shift ;;
        --no-linger) DO_LINGER=0; shift ;;
        --dry-run)   DRY_RUN=1; shift ;;
        --uninstall) ACTION=uninstall; shift ;;
        --purge)     PURGE=1; shift ;;
        -h|--help)   usage; exit 0 ;;
        *)           die "unknown option '$1' (see --help)" ;;
    esac
done

if [ -n "$INSTANCES" ]; then
    case "$INSTANCES" in
        *[!0-9]*) die "--instances takes a number (0-16)" ;;
    esac
    [ "$INSTANCES" -le 16 ] || die "--instances: at most 16"
fi
if [ "$PURGE" = 1 ] && [ "$ACTION" != uninstall ]; then
    die "--purge goes with --uninstall"
fi

# --- platform ----------------------------------------------------------------------
OS="$(uname -s)"
if [ -n "${MAKTABA_NODE_OS:-}" ]; then
    # Test hook: render another OS's files on this one. Dry runs only.
    [ "$DRY_RUN" = 1 ] || die "MAKTABA_NODE_OS is only honoured with --dry-run"
    OS="$MAKTABA_NODE_OS"
fi
case "$OS" in
    Darwin|darwin) OS=darwin ;;
    Linux|linux)   OS=linux ;;
    *) die "unsupported OS '$OS': this script is for macOS and Linux (Windows: windows/install-node.ps1)" ;;
esac
ARCH="$(uname -m)"

if [ "$SCOPE" = system ] && [ "$OS" != linux ]; then
    die "--system is for Linux; on macOS the worker is a LaunchAgent of the logged-in user (see README.md)"
fi
if [ -n "$RUN_AS" ] && [ "$SCOPE" != system ]; then
    die "--run-as goes with --system"
fi
if [ "$DRY_RUN" != 1 ]; then
    if [ "$SCOPE" = system ] && [ "$(id -u)" -ne 0 ]; then
        die "--system needs root: sudo $0 --system ..."
    fi
    if [ "$SCOPE" = user ] && [ "$(id -u)" -eq 0 ]; then
        die "run this as the user the worker should run as (not root), or use --system on Linux"
    fi
fi

# --- dry-run plumbing -----------------------------------------------------------------
if [ "$DRY_RUN" = 1 ]; then
    DRY_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/maktaba-node-dry-run.XXXXXX")"
fi

quote_cmd() {
    local out="" a
    for a in "$@"; do out="$out $(printf '%q' "$a")"; done
    printf '%s' "${out# }"
}

# run CMD...: run it, or print it in a dry run.
run() {
    if [ "$DRY_RUN" = 1 ]; then
        printf '  + %s\n' "$(quote_cmd "$@")"
        return 0
    fi
    "$@"
}

# target PATH: where a file for PATH is written (under DRY_ROOT in a dry run).
target() {
    if [ "$DRY_RUN" = 1 ]; then printf '%s%s' "$DRY_ROOT" "$1"; else printf '%s' "$1"; fi
}

# write_file DEST MODE <content: atomically, creating the directory.
write_file() {
    local dest tmp
    dest="$(target "$1")"
    mkdir -p "$(dirname "$dest")"
    tmp="$dest.tmp.$$"
    cat >"$tmp"
    chmod "$2" "$tmp"
    mv -f "$tmp" "$dest"
}

copy_file() { write_file "$2" "$3" <"$1"; }

remove() {
    local p
    for p in "$@"; do
        if [ -e "$p" ] || [ -L "$p" ]; then run rm -rf -- "$p"; fi
    done
}

# --- paths ---------------------------------------------------------------------------
SYSTEMCTL=()
CONFIG_HOME=""
case "$OS/$SCOPE" in
    darwin/user)
        CONF_DIR="$HOME/Library/Application Support/Maktaba"
        NODE_DIR="$CONF_DIR/node"
        LOG_DIR="$HOME/Library/Logs/Maktaba"
        AGENTS_DIR="$HOME/Library/LaunchAgents"
        LINK_DIR="$HOME/.local/bin"
        ;;
    linux/user)
        # The user manager's XDG_CONFIG_HOME, which can differ from this shell's: the
        # worker (and so the join below) keeps node.json under it, and the manager reads
        # units from it.
        if command -v systemctl >/dev/null 2>&1; then
            CONFIG_HOME="$(systemctl --user show-environment 2>/dev/null \
                | sed -n 's/^XDG_CONFIG_HOME=//p' || true)"
        fi
        CONFIG_HOME="${CONFIG_HOME:-$HOME/.config}"
        CONF_DIR="$HOME/.config/maktaba"             # node*.env: fixed, the units name it with %h
        NODE_DIR="$HOME/.local/share/maktaba/node"   # fixed: the units name it with %h
        UNIT_DIR="$CONFIG_HOME/systemd/user"
        WANTS_DIR="$UNIT_DIR/default.target.wants"
        LINK_DIR="$HOME/.local/bin"
        SYSTEMCTL=(systemctl --user)
        ;;
    linux/system)
        CONF_DIR=/etc/maktaba
        NODE_DIR=/opt/maktaba/node
        UNIT_DIR=/etc/systemd/system
        WANTS_DIR="$UNIT_DIR/multi-user.target.wants"
        LINK_DIR=/usr/local/bin
        SYSTEMCTL=(systemctl)
        umask 022
        ;;
esac
VENV="$NODE_DIR/venv"
PY="$VENV/bin/python"
CLI="$NODE_DIR/bin/maktaba-pipeline"
INSTALL_CONF="$NODE_DIR/install.conf"

# read_kv FILE KEY: a KEY=VALUE line's value (never sourced).
read_kv() {
    [ -f "$1" ] || return 0
    sed -n "s/^$2=//p" "$1" | tail -n 1
}

# Remembered choices from the last install (update = run again without them).
if [ -z "$EXTRAS" ]; then EXTRAS="$(read_kv "$INSTALL_CONF" EXTRAS)"; fi
if [ -z "$SOURCE" ]; then SOURCE="$(read_kv "$INSTALL_CONF" SOURCE)"; fi
if [ "$SCOPE" = system ] && [ -z "$RUN_AS" ]; then RUN_AS="$(read_kv "$INSTALL_CONF" RUN_AS)"; fi
if [ "$SCOPE" = system ]; then RUN_AS="${RUN_AS:-maktaba}"; fi
if [ -z "$EXTRAS" ]; then
    if [ "$OS" = darwin ] && [ "$ARCH" = arm64 ]; then EXTRAS=stt; else EXTRAS=none; fi
fi
if [ -z "$SOURCE" ]; then
    SOURCE="$(cd "$SCRIPT_DIR/../../.." && pwd)/pipeline"
fi

# --- environment the services see -------------------------------------------------------
ENV_ARGS=()

# read_env_file FILE: append its KEY=VALUE lines to ENV_ARGS (the services' format).
read_env_file() {
    local line key value
    [ -f "$1" ] || return 0
    while IFS= read -r line || [ -n "$line" ]; do
        line="${line#"${line%%[![:space:]]*}"}"
        case "$line" in
            ''|'#'*) continue ;;
            'export '*) line="${line#export }" ;;
        esac
        case "$line" in *=*) ;; *) continue ;; esac
        key="${line%%=*}"
        value="${line#*=}"
        case "$key" in ''|[0-9]*|*[!A-Za-z0-9_]*) continue ;; esac
        case "$value" in
            \"*\") value="${value#\"}"; value="${value%\"}" ;;
            \'*\') value="${value#\'}"; value="${value%\'}" ;;
        esac
        ENV_ARGS+=("$key=$value")
    done <"$1"
}

# as_service CMD...: run CMD as the worker would run: its account, node.env applied,
# MAKTABA_DATABASE_URL unset.
as_service() {
    local pre=() home
    ENV_ARGS=()
    read_env_file "$CONF_DIR/node.env"
    if [ "$SCOPE" = system ]; then
        home="$(getent passwd "$RUN_AS" 2>/dev/null | cut -d: -f6 || true)"
        if command -v sudo >/dev/null 2>&1; then
            pre=(sudo -u "$RUN_AS" -H)
        else
            pre=(runuser -u "$RUN_AS" --)
        fi
        if [ -n "$home" ]; then ENV_ARGS=("HOME=$home" ${ENV_ARGS[@]+"${ENV_ARGS[@]}"}); fi
    elif [ "$OS" = linux ]; then
        ENV_ARGS=("XDG_CONFIG_HOME=$CONFIG_HOME" ${ENV_ARGS[@]+"${ENV_ARGS[@]}"})
    fi
    # From the account's home, as the service: `python -m` puts the working directory
    # first on sys.path, and the worker's account may not be able to enter this one.
    (
        cd "${home:-$HOME}" 2>/dev/null || cd /
        run ${pre[@]+"${pre[@]}"} env -u MAKTABA_DATABASE_URL ${ENV_ARGS[@]+"${ENV_ARGS[@]}"} "$@"
    )
}

is_joined() {
    [ "$DRY_RUN" = 1 ] && return 1
    [ -x "$PY" ] || return 1
    as_service "$PY" "$NODE_DIR/node_check.py" >/dev/null 2>&1
}

# --- instances -------------------------------------------------------------------------
# installed_instances: the extra workers installed now, one name per line.
installed_instances() {
    local f n
    if [ "$OS" = darwin ]; then
        for f in "$AGENTS_DIR"/com.maktaba.node.*.plist; do
            [ -e "$f" ] || continue
            n="${f##*/com.maktaba.node.}"
            printf '%s\n' "${n%.plist}"
        done
    else
        for f in "$WANTS_DIR"/maktaba-node@*.service; do
            [ -e "$f" ] || [ -L "$f" ] || continue
            n="${f##*/maktaba-node@}"
            printf '%s\n' "${n%.service}"
        done
    fi
}

WANTED=()     # instances to have after this run
UNWANTED=()   # installed instances to remove
plan_instances() {
    local inst i keep
    if [ -z "$INSTANCES" ]; then
        while IFS= read -r inst; do
            [ -n "$inst" ] && WANTED+=("$inst")
        done < <(installed_instances)
        return 0
    fi
    i=1
    while [ "$i" -le "$INSTANCES" ]; do WANTED+=("$i"); i=$((i + 1)); done
    while IFS= read -r inst; do
        [ -n "$inst" ] || continue
        keep=0
        for i in ${WANTED[@]+"${WANTED[@]}"}; do
            if [ "$i" = "$inst" ]; then keep=1; fi
        done
        if [ "$keep" = 0 ]; then UNWANTED+=("$inst"); fi
    done < <(installed_instances)
}

# --- Python environment ----------------------------------------------------------------
find_uv() {
    local c sudo_home=""
    # under sudo, PATH and HOME are root's: also look where the invoking user put uv
    if [ -n "${SUDO_USER:-}" ]; then
        sudo_home="$(getent passwd "$SUDO_USER" 2>/dev/null | cut -d: -f6 || true)"
    fi
    for c in "$(command -v uv 2>/dev/null || true)" "$HOME/.local/bin/uv" \
             "$HOME/.cargo/bin/uv" ${sudo_home:+"$sudo_home/.local/bin/uv"} \
             ${sudo_home:+"$sudo_home/.cargo/bin/uv"} \
             /opt/homebrew/bin/uv /usr/local/bin/uv /usr/bin/uv; do
        if [ -n "$c" ] && [ -x "$c" ]; then printf '%s' "$c"; return 0; fi
    done
    return 1
}

find_python() {
    local c p
    for c in python3.12 python3.13 python3.14 python3; do
        p="$(command -v "$c" 2>/dev/null)" || continue
        if "$p" -c 'import sys; sys.exit(sys.version_info < (3, 12))' 2>/dev/null; then
            printf '%s' "$p"
            return 0
        fi
    done
    return 1
}

build_venv() {
    local uv py extra_args=() pip_spec="$SOURCE" pyver="" e parts=()
    if [ "$EXTRAS" != none ]; then
        IFS=, read -r -a parts <<<"$EXTRAS"
        for e in ${parts[@]+"${parts[@]}"}; do
            if [ -n "$e" ]; then extra_args+=(--extra "$e"); fi
        done
        pip_spec="${SOURCE}[${EXTRAS}]"
    fi
    if [ -f "$SOURCE/.python-version" ]; then pyver="$(tr -d '[:space:]' <"$SOURCE/.python-version")"; fi
    run mkdir -p "$NODE_DIR"
    if uv="$(find_uv)"; then
        info "Building the worker's Python environment with uv ($VENV)"
        local uv_env=("UV_PROJECT_ENVIRONMENT=$VENV")
        if [ "$SCOPE" = system ]; then
            # a uv-managed Python must sit where the service account can read it
            uv_env+=("UV_PYTHON_INSTALL_DIR=$NODE_DIR/python")
        fi
        # --no-editable: the service runs a snapshot, not the checkout being edited;
        # --reinstall-package: uv would otherwise keep a stale build of an unchanged version.
        run env "${uv_env[@]}" "$uv" sync --project "$SOURCE" --frozen --no-dev --no-editable \
            --reinstall-package maktaba-pipeline ${pyver:+--python "$pyver"} \
            ${extra_args[@]+"${extra_args[@]}"}
    elif py="$(find_python)"; then
        warn "uv not found: using $py and pip (the lock file is not enforced)."
        warn "Install uv for reproducible installs: https://docs.astral.sh/uv/"
        if [ ! -x "$PY" ]; then run "$py" -m venv "$VENV"; fi
        run "$PY" -m pip install --quiet --upgrade pip
        run "$PY" -m pip install --quiet "$pip_spec"
        run "$PY" -m pip install --quiet --force-reinstall --no-deps "$SOURCE"
    else
        die "neither uv nor Python 3.12+ found. Install uv (https://docs.astral.sh/uv/) and run this again."
    fi
}

# --- small files -------------------------------------------------------------------------
sq() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }

install_helpers() {
    copy_file "$SCRIPT_DIR/node_check.py" "$NODE_DIR/node_check.py" 0644
    if [ "$OS" = darwin ]; then
        copy_file "$SCRIPT_DIR/launchd/maktaba-node-run" "$NODE_DIR/bin/maktaba-node-run" 0755
    fi
    # $CLI is what the services run (`maktaba-pipeline run --node`) and what PATH gets:
    # the venv's own maktaba-pipeline, or a stand-in for a pipeline without that entry
    # point.
    run mkdir -p "$NODE_DIR/bin"
    if [ -x "$VENV/bin/maktaba-pipeline" ] || [ "$DRY_RUN" = 1 ]; then
        run ln -sfn "$VENV/bin/maktaba-pipeline" "$CLI"
    else
        warn "the pipeline has no maktaba-pipeline entry point: using a stand-in ($CLI)"
        write_file "$CLI" 0755 <<EOF
#!/bin/sh
# Stand-in for the venv's maktaba-pipeline entry point (written by install-node.sh)
exec $(sq "$PY") -m maktaba_pipeline "\$@"
EOF
    fi
    if [ -d "$LINK_DIR" ]; then
        local link="$LINK_DIR/maktaba-pipeline"
        if [ ! -e "$link" ] && [ ! -L "$link" ]; then
            run ln -s "$CLI" "$link"
        elif [ -L "$link" ] && [ "$(readlink "$link")" = "$CLI" ]; then
            :
        else
            warn "$link exists and is not this node's; use $CLI"
        fi
    fi
    write_file "$INSTALL_CONF" 0644 <<EOF
# install-node.sh remembers these for updates
EXTRAS=$EXTRAS
SOURCE=$SOURCE
RUN_AS=$RUN_AS
EOF
}

# The services run `maktaba-pipeline run --node` (exit 78 until the machine has joined,
# 77 once the home has removed it); a pipeline without that option would fail on every
# start, so refuse it here.
check_node_mode() {
    local help
    [ "$DRY_RUN" = 1 ] && return 0
    help="$("$CLI" run --help 2>/dev/null || true)"
    case "$help" in
        *--node*) ;;
        *) die "the pipeline at $SOURCE has no 'run --node' yet: update the checkout and run this again" ;;
    esac
}

service_path() {
    if [ "$OS" = darwin ]; then
        printf '%s' /opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
    else
        printf '%s' /usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
    fi
}

ensure_env_file() {
    local env_file="$CONF_DIR/node.env" written mode=0600 ff probe add=""
    written="$(target "$env_file")"
    if [ "$SCOPE" = system ]; then mode=0640; fi
    if [ ! -f "$written" ]; then
        info "Writing default settings to $env_file"
        copy_file "$SCRIPT_DIR/node.env.example" "$env_file" "$mode"
    fi
    ff="$(command -v ffmpeg 2>/dev/null || true)"
    if [ -z "$ff" ]; then
        warn "ffmpeg not found: stages that read media will report it missing."
        warn "Install it (brew install ffmpeg / apt install ffmpeg), or set MAKTABA_FFMPEG in $env_file."
        return 0
    fi
    case ":$(service_path):" in *":$(dirname "$ff"):"*) return 0 ;; esac
    if grep -q '^MAKTABA_FFMPEG=' "$written"; then return 0; fi
    add="MAKTABA_FFMPEG=$ff"
    probe="$(dirname "$ff")/ffprobe"
    if [ -x "$probe" ] && ! grep -q '^MAKTABA_FFPROBE=' "$written"; then
        add="$add
MAKTABA_FFPROBE=$probe"
    fi
    info "ffmpeg is at $ff, off the service's PATH: recording it in $env_file"
    printf '\n# found by install-node.sh\n%s\n' "$add" >>"$written"
}

ensure_run_as() {
    [ "$SCOPE" = system ] || return 0
    if getent passwd "$RUN_AS" >/dev/null 2>&1; then return 0; fi
    [ "$RUN_AS" = maktaba ] || die "user '$RUN_AS' does not exist"
    info "Creating the maktaba system user"
    if ! getent group maktaba >/dev/null 2>&1; then run groupadd --system maktaba; fi
    run useradd --system --gid maktaba --home-dir /var/lib/maktaba --no-create-home \
        --shell /usr/sbin/nologin --comment "Maktaba node worker" maktaba
    run mkdir -p /var/lib/maktaba
    run chown maktaba:maktaba /var/lib/maktaba
    run chmod 0750 /var/lib/maktaba
}

join_if_needed() {
    if is_joined; then
        info "This machine has joined its home already"
        return 0
    fi
    if [ "$DO_JOIN" = 0 ]; then
        if [ "$SCOPE" = system ]; then
            warn "Not joined yet (--no-join): the worker waits until you run, as its account:"
            warn "  sudo -u $RUN_AS -H $CLI join"
        else
            warn "Not joined yet (--no-join): the worker waits until you run: maktaba-pipeline join"
        fi
        return 0
    fi
    local args=(join)
    if [ -n "$HOME_URL" ]; then args+=(--home "$HOME_URL"); fi
    if [ -n "$NODE_NAME" ]; then args+=(--name "$NODE_NAME"); fi
    info "Joining a Maktaba home. On the home: Admin -> Machines, turn on 'Accept new"
    info "machines', then type the code shown below into this machine's request."
    # join asks which home when it finds several: give it the terminal even when this
    # script's stdin is a pipe.
    if [ -t 0 ] || [ "$DRY_RUN" = 1 ] || ! (exec </dev/tty) 2>/dev/null; then
        as_service "$CLI" "${args[@]}"
    else
        as_service "$CLI" "${args[@]}" </dev/tty
    fi
    if [ "$DRY_RUN" != 1 ] && ! is_joined; then
        die "the join did not complete; run this again (or pass --no-join to install anyway)"
    fi
}

# --- macOS: launchd -----------------------------------------------------------------------
GUI_DOMAIN="gui/$(id -u)"

xml_escape() { printf '%s' "$1" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'; }
sed_value() { xml_escape "$1" | sed -e 's/[\\|&]/\\&/g'; }

# render_plist INSTANCE: the plist for one worker, on stdout.
render_plist() {
    local inst="$1" label base slot="" stages="" exclude=""
    if [ "$inst" = main ]; then
        label=com.maktaba.node
        base=node
        if [ "${#WANTED[@]}" -gt 0 ]; then exclude=transcribe; fi
    else
        label="com.maktaba.node.$inst"
        base="node@$inst"
        slot="stt-$inst"
        stages=transcribe
    fi
    sed -e "s|__LABEL__|$(sed_value "$label")|g" \
        -e "s|__RUNNER__|$(sed_value "$NODE_DIR/bin/maktaba-node-run")|g" \
        -e "s|__INSTANCE__|$(sed_value "$inst")|g" \
        -e "s|__NODE_DIR__|$(sed_value "$NODE_DIR")|g" \
        -e "s|__CONF_DIR__|$(sed_value "$CONF_DIR")|g" \
        -e "s|__LOG_DIR__|$(sed_value "$LOG_DIR")|g" \
        -e "s|__STT_SLOT__|$(sed_value "$slot")|g" \
        -e "s|__STAGES__|$(sed_value "$stages")|g" \
        -e "s|__EXCLUDE_STAGES__|$(sed_value "$exclude")|g" \
        -e "s|__HOME__|$(sed_value "$HOME")|g" \
        -e "s|__STDOUT__|$(sed_value "$LOG_DIR/$base.log")|g" \
        -e "s|__STDERR__|$(sed_value "$LOG_DIR/$base.err.log")|g" \
        "$SCRIPT_DIR/launchd/com.maktaba.node.plist"
}

launchd_unload() {
    local label="$1" i=0
    run launchctl bootout "$GUI_DOMAIN/$label" 2>/dev/null || true
    [ "$DRY_RUN" = 1 ] && return 0
    while launchctl print "$GUI_DOMAIN/$label" >/dev/null 2>&1 && [ "$i" -lt 20 ]; do
        sleep 0.5
        i=$((i + 1))
    done
}

launchd_load() {
    local label="$1" plist="$2"
    run launchctl enable "$GUI_DOMAIN/$label" || true
    if ! run launchctl bootstrap "$GUI_DOMAIN" "$plist"; then
        warn "could not load $label now (no desktop session?): it starts at your next login"
    fi
}

install_launchd() {
    local inst plist label rendered
    run mkdir -p "$LOG_DIR"
    for inst in ${UNWANTED[@]+"${UNWANTED[@]}"}; do
        label="com.maktaba.node.$inst"
        info "Removing worker $inst"
        launchd_unload "$label"
        remove "$AGENTS_DIR/$label.plist"
    done
    for inst in main ${WANTED[@]+"${WANTED[@]}"}; do
        if [ "$inst" = main ]; then label=com.maktaba.node; else label="com.maktaba.node.$inst"; fi
        plist="$AGENTS_DIR/$label.plist"
        render_plist "$inst" | write_file "$plist" 0644
        rendered="$(target "$plist")"
        if command -v plutil >/dev/null 2>&1; then
            plutil -lint -s "$rendered" || die "rendered $rendered does not parse"
        fi
        if [ "$DO_START" = 1 ]; then
            info "Starting $label"
            launchd_unload "$label"
            launchd_load "$label" "$plist"
        else
            info "Installed $label (starts at next login; --no-start)"
        fi
    done
}

uninstall_launchd() {
    local f label
    for f in "$AGENTS_DIR"/com.maktaba.node.plist "$AGENTS_DIR"/com.maktaba.node.*.plist; do
        [ -e "$f" ] || continue
        label="$(basename "$f" .plist)"
        info "Stopping and removing $label"
        launchd_unload "$label"
        remove "$f"
    done
}

# --- Linux: systemd -------------------------------------------------------------------------
check_systemd() {
    [ "$DRY_RUN" = 1 ] && return 0
    command -v systemctl >/dev/null 2>&1 || die "systemctl not found: this needs systemd"
    if [ "$SCOPE" = user ] && ! systemctl --user show-environment >/dev/null 2>&1; then
        die "no systemd user session here. Log in as this user directly (desktop or ssh, not su/sudo), or use --system."
    fi
}

install_systemd() {
    local src_dir="$SCRIPT_DIR/systemd/$SCOPE" inst group dropin
    copy_file "$src_dir/maktaba-node.service" "$UNIT_DIR/maktaba-node.service" 0644
    copy_file "$src_dir/maktaba-node@.service" "$UNIT_DIR/maktaba-node@.service" 0644
    if [ "$SCOPE" = system ]; then
        if [ "$DRY_RUN" = 1 ]; then group="$RUN_AS"; else group="$(id -gn "$RUN_AS")"; fi
        for dropin in maktaba-node.service.d maktaba-node@.service.d; do
            write_file "$UNIT_DIR/$dropin/10-run-as.conf" 0644 <<EOF
# Written by install-node.sh --run-as: the account this machine's worker runs as.
[Service]
User=$RUN_AS
Group=$group
EOF
        done
    fi
    dropin="$UNIT_DIR/maktaba-node.service.d/20-transcription-workers.conf"
    if [ "${#WANTED[@]}" -gt 0 ]; then
        write_file "$dropin" 0644 <<'EOF'
# Written by install-node.sh: maktaba-node@<N> workers do the transcription here.
[Service]
Environment=MAKTABA_PIPELINE_EXCLUDE_STAGES=transcribe
EOF
    else
        remove "$dropin"
    fi

    for inst in ${UNWANTED[@]+"${UNWANTED[@]}"}; do
        info "Removing worker $inst"
        run "${SYSTEMCTL[@]}" disable --now "maktaba-node@$inst.service" || true
    done
    run "${SYSTEMCTL[@]}" daemon-reload
    run "${SYSTEMCTL[@]}" enable maktaba-node.service
    for inst in ${WANTED[@]+"${WANTED[@]}"}; do
        run "${SYSTEMCTL[@]}" enable "maktaba-node@$inst.service"
    done
    if [ "$DO_START" = 1 ]; then
        info "Starting the workers"
        run "${SYSTEMCTL[@]}" restart maktaba-node.service
        for inst in ${WANTED[@]+"${WANTED[@]}"}; do
            run "${SYSTEMCTL[@]}" restart "maktaba-node@$inst.service"
        done
    fi

    if [ "$SCOPE" = user ] && [ "$DO_LINGER" = 1 ]; then
        local me
        me="$(id -un)"
        if [ "$DRY_RUN" = 1 ] || [ "$(loginctl show-user "$me" --property=Linger --value 2>/dev/null || true)" != yes ]; then
            info "Turning on lingering for $me, so the worker starts at boot"
            run loginctl enable-linger "$me" \
                || warn "could not turn on lingering (ask an admin: sudo loginctl enable-linger $me); the worker starts at login until then"
        fi
    fi
}

uninstall_systemd() {
    local units=(maktaba-node.service) inst
    while IFS= read -r inst; do
        [ -n "$inst" ] && units+=("maktaba-node@$inst.service")
    done < <(installed_instances)
    if [ "$DRY_RUN" = 1 ] || command -v systemctl >/dev/null 2>&1; then
        info "Stopping and disabling ${units[*]}"
        run "${SYSTEMCTL[@]}" disable --now "${units[@]}" || true
        # instances started by hand without being enabled
        run "${SYSTEMCTL[@]}" stop 'maktaba-node@*.service' || true
    fi
    remove "$UNIT_DIR/maktaba-node.service" "$UNIT_DIR/maktaba-node@.service" \
        "$UNIT_DIR/maktaba-node.service.d" "$UNIT_DIR/maktaba-node@.service.d"
    if [ "$DRY_RUN" = 1 ] || command -v systemctl >/dev/null 2>&1; then
        run "${SYSTEMCTL[@]}" daemon-reload || true
        run "${SYSTEMCTL[@]}" reset-failed "${units[@]}" 2>/dev/null || true
    fi
}

# --- summary ---------------------------------------------------------------------------------
summary() {
    local inst
    echo
    info "Done."
    if [ "$SCOPE" = system ]; then
        echo "  Node config:  ~$RUN_AS/.config/maktaba/node.json"
    elif [ "$OS" = linux ]; then
        echo "  Node config:  $CONFIG_HOME/maktaba/node.json"
    else
        echo "  Node config:  $CONF_DIR/node.json"
    fi
    echo "  Settings:     $CONF_DIR/node.env (node@<N>.env per extra worker)"
    echo "  Python:       $VENV"
    if [ "$SCOPE" = system ]; then
        echo "  Command:      sudo -u $RUN_AS -H $CLI ... (as the worker's account: join, cluster logs, ...)"
    else
        echo "  Command:      $CLI (join, cluster logs, ...)"
    fi
    if [ "$OS" = darwin ]; then
        echo "  Status:       launchctl print $GUI_DOMAIN/com.maktaba.node | grep -E 'state|last exit'"
        echo "  Logs:         tail -f \"$LOG_DIR/node.log\" \"$LOG_DIR/node.err.log\""
        echo "  Restart:      launchctl kickstart -k $GUI_DOMAIN/com.maktaba.node"
        for inst in ${WANTED[@]+"${WANTED[@]}"}; do
            echo "  Worker $inst:     com.maktaba.node.$inst, logs node@$inst.log"
        done
    else
        local sc="systemctl" jc="journalctl"
        if [ "$SCOPE" = user ]; then sc="systemctl --user"; jc="journalctl --user"; fi
        echo "  Status:       $sc status maktaba-node 'maktaba-node@*'"
        echo "  Logs:         $jc -u maktaba-node -f"
        echo "  Restart:      $sc restart maktaba-node"
        for inst in ${WANTED[@]+"${WANTED[@]}"}; do
            echo "  Worker $inst:     maktaba-node@$inst.service"
        done
    fi
    if [ "$DRY_RUN" = 1 ]; then
        echo
        info "Dry run: nothing was changed. Rendered files are under $DRY_ROOT"
    fi
}

# --- main ----------------------------------------------------------------------------------------
do_install() {
    [ -f "$SOURCE/pyproject.toml" ] || die "no pipeline source at $SOURCE (pass --source DIR)"
    grep -q '^name = "maktaba-pipeline"' "$SOURCE/pyproject.toml" \
        || die "$SOURCE is not the maktaba-pipeline project"
    if [ "$OS" = linux ]; then check_systemd; fi
    plan_instances
    if [ "${#WANTED[@]}" -gt 0 ] && ! { [ "$OS" = darwin ] && [ "$ARCH" = arm64 ]; }; then
        warn "transcription runs on Apple Silicon only today: the extra workers claim nothing"
        warn "here unless node@<N>.env gives them other stages (MAKTABA_PIPELINE_STAGES)."
    fi

    info "Maktaba node: $OS/$ARCH, ${SCOPE} scope${RUN_AS:+, runs as $RUN_AS}, extras: $EXTRAS, extra workers: ${#WANTED[@]}"
    ensure_run_as
    build_venv
    install_helpers
    check_node_mode
    ensure_env_file
    join_if_needed
    if [ "$OS" = darwin ]; then install_launchd; else install_systemd; fi
    summary
}

do_uninstall() {
    if [ "$OS" = darwin ]; then uninstall_launchd; else uninstall_systemd; fi
    local link="$LINK_DIR/maktaba-pipeline"
    if [ -L "$link" ] && [ "$(readlink "$link")" = "$CLI" ]; then
        remove "$link"
    fi
    info "Removing the worker's Python environment ($NODE_DIR)"
    remove "$NODE_DIR"
    if [ "$PURGE" = 1 ]; then
        info "Removing settings (node*.env) and logs"
        remove "$CONF_DIR/node.env"
        local f
        for f in "$CONF_DIR"/node@*.env; do remove "$f"; done
        if [ "$OS" = darwin ]; then
            for f in "$LOG_DIR"/node.log* "$LOG_DIR"/node.err.log* "$LOG_DIR"/node@*.log*; do
                remove "$f"
            done
        fi
    fi
    echo
    info "Removed. The node config (node.json) is kept: it is this machine's identity on the"
    info "home. Remove the machine on the home (Admin -> Machines), then delete that file to"
    info "forget the home here."
    if [ "$DRY_RUN" = 1 ]; then info "Dry run: nothing was changed."; fi
}

if [ "$ACTION" = uninstall ]; then do_uninstall; else do_install; fi
