#!/usr/bin/env bash
# Maktaba node installer for macOS and Linux (Epic 31, multi-instance).
#
# Makes this machine a worker of a Maktaba home: builds the pipeline's Python
# environment (from a release's wheel, or from a checkout's pipeline/), joins the home when the machine has not joined yet (`maktaba-pipeline
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
# Where the pipeline comes from, first match wins:
#   --from-wheel / --source                  as given
#   the last install's choice                (install.conf; an update keeps it)
#   release.conf beside this script          the release's wheel: this script came from
#                                            a maktaba-node-<version> archive
#   pipeline/ of the checkout holding it     the git checkout this script is in
#
# Running it again updates in place: reinstalls the pipeline (a newer release's
# archive installs that release; a checkout rebuilds after a `git pull`), re-renders the
# units and restarts the workers. See README.md.
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
#   --from-wheel URL|PATH
#                    install the pipeline from this wheel (maktaba_pipeline-*.whl), a
#                    file or an http(s) URL (default: the release this script came
#                    with; remembered for updates)
#   --sha256 HEX     with --from-wheel: the wheel's SHA-256; a file that differs is
#                    refused (remembered; a wheel downloaded without one is pinned to
#                    the digest of its first download)
#   --constraints URL|PATH
#                    with --from-wheel: pin its dependencies with this constraints
#                    file (maktaba-pipeline-<version>-constraints.txt of the release;
#                    the release's own when installing the release)
#   --constraints-sha256 HEX
#                    with --constraints: its SHA-256 (remembered, pinned the same way)
#   --source DIR     build the pipeline from this source directory instead (default
#                    in a checkout: pipeline/ of the repo holding this script;
#                    remembered for updates)
#   --system         Linux: system units, started at boot with no login (needs root)
#   --run-as USER    with --system: the account the worker runs as (default: maktaba,
#                    created when missing; remembered for updates)
#   --no-join        do not join now; the service waits and starts once joined
#   --no-start       install and enable, but do not start or restart anything
#   --no-linger      Linux user units: leave lingering off (the worker then starts at
#                    login instead of at boot)
#   --full           then make this machine a full node (Epic 35 story 35.8): runs
#                    `maktaba-server setup --node`, which verifies the home's release
#                    (Sigstore + checksums, in process), installs it side by side, keeps
#                    node.json and this worker's Python environment, registers the
#                    maktaba-server service and removes the worker units once it runs
#   --server-dir DIR with --full: a directory holding the release (its assets, or an
#                    extracted maktaba-server archive with release.json, checksums.txt
#                    and checksums.txt.sigstore.json); default: setup --node fetches the
#                    home's version itself. maktaba-server is taken from DIR, else PATH
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
FROM_WHEEL=""
CONSTRAINTS=""
WHEEL_SHA_ARG=""
CONSTRAINTS_SHA_ARG=""
FROM=""        # where the pipeline comes from: release, wheel or source
SCOPE=user
RUN_AS=""
DO_JOIN=1
DO_START=1
DO_LINGER=1
PURGE=0
DRY_RUN=0
FULL=0
SERVER_DIR=""
DRY_ROOT=""

info()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33mwarn:\033[0m %s\n' "$*" >&2; }
die()   { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

# url_has_credentials SPEC: an http(s) URL with user info (user:password@host) or a query
# string (a token, a signed URL's signature). Such a URL is neither logged nor remembered.
url_has_credentials() {
    local rest auth
    case "$1" in http://*|https://*) ;; *) return 1 ;; esac
    rest="${1#*://}"
    auth="${rest%%/*}"
    case "$auth" in *@*) return 0 ;; esac
    case "$1" in *\?*) return 0 ;; esac
    return 1
}

# redact_url SPEC: SPEC for a message, user info and query string replaced by ***.
redact_url() {
    local scheme rest auth path=""
    case "$1" in http://*|https://*) ;; *) printf '%s' "$1"; return 0 ;; esac
    scheme="${1%%://*}"
    rest="${1#*://}"
    auth="${rest%%/*}"
    if [ "$auth" != "$rest" ]; then path="/${rest#*/}"; fi
    case "$auth" in *@*) auth="***@${auth##*@}" ;; esac
    case "$path" in *[?#]*) path="${path%%[?#]*}?***" ;; esac
    printf '%s://%s%s' "$scheme" "$auth" "$path"
}

usage() {
    awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"
}

need_value() { if [ $# -lt 2 ] || [ -z "$2" ]; then die "$1 needs a value"; fi; }

while [ $# -gt 0 ]; do
    case "$1" in
        --home)      need_value "$@"; HOME_URL="$2"; shift 2 ;;
        --name)      need_value "$@"; NODE_NAME="$2"; shift 2 ;;
        --instances) need_value "$@"; INSTANCES="$2"; shift 2 ;;
        --extras)    need_value "$@"; EXTRAS="$2"; shift 2 ;;
        --source)    need_value "$@"; SOURCE="$2"; shift 2 ;;
        --from-wheel)  need_value "$@"; FROM_WHEEL="$2"; shift 2 ;;
        --constraints) need_value "$@"; CONSTRAINTS="$2"; shift 2 ;;
        --sha256)      need_value "$@"; WHEEL_SHA_ARG="$2"; shift 2 ;;
        --constraints-sha256) need_value "$@"; CONSTRAINTS_SHA_ARG="$2"; shift 2 ;;
        --system)    SCOPE=system; shift ;;
        --run-as)    need_value "$@"; RUN_AS="$2"; shift 2 ;;
        --no-join)   DO_JOIN=0; shift ;;
        --no-start)  DO_START=0; shift ;;
        --no-linger) DO_LINGER=0; shift ;;
        --dry-run)   DRY_RUN=1; shift ;;
        --full)      FULL=1; shift ;;
        --server-dir) need_value "$@"; SERVER_DIR="$2"; shift 2 ;;
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
if [ -n "$FROM_WHEEL" ] && [ -n "$SOURCE" ]; then
    die "--from-wheel and --source: give one (a wheel, or a source directory to build)"
fi
if [ -n "$CONSTRAINTS" ] && [ -n "$SOURCE" ]; then
    die "--constraints goes with --from-wheel (a source build uses its uv.lock)"
fi
# check_hex NAME VALUE: VALUE, lower-cased, if it is a SHA-256 in hex.
check_hex() {
    local v
    v="$(printf '%s' "$2" | tr 'A-F' 'a-f')"
    case "$v" in
        *[!0-9a-f]*) die "$1 takes a SHA-256 in hex (64 characters): $2" ;;
    esac
    [ "${#v}" -eq 64 ] || die "$1 takes a SHA-256 in hex (64 characters): $2"
    printf '%s' "$v"
}
if [ -n "$WHEEL_SHA_ARG" ]; then
    [ -n "$FROM_WHEEL" ] || die "--sha256 goes with --from-wheel (a release's wheel has its digest in release.conf)"
    WHEEL_SHA_ARG="$(check_hex --sha256 "$WHEEL_SHA_ARG")"
fi
if [ -n "$CONSTRAINTS_SHA_ARG" ]; then
    [ -n "$CONSTRAINTS" ] || die "--constraints-sha256 goes with --constraints"
    CONSTRAINTS_SHA_ARG="$(check_hex --constraints-sha256 "$CONSTRAINTS_SHA_ARG")"
fi

# --- platform ----------------------------------------------------------------------
OS="$(uname -s)"
if [ -n "${MAKTABA_NODE_OS:-}" ]; then
    # Test hook: render another OS's files on this one. Dry runs only.
    [ "$DRY_RUN" = 1 ] || die "MAKTABA_NODE_OS is only honoured with --dry-run"
    OS="$MAKTABA_NODE_OS"
fi
if [ -n "${MAKTABA_NODE_NO_UV:-}" ] && [ "$DRY_RUN" != 1 ]; then
    die "MAKTABA_NODE_NO_UV is only honoured with --dry-run"
fi
# Test hook: MAKTABA_NODE_HASH=python (skip sha256sum and shasum) or none (no way to hash).
if [ -n "${MAKTABA_NODE_HASH:-}" ] && [ "$DRY_RUN" != 1 ]; then
    die "MAKTABA_NODE_HASH is only honoured with --dry-run"
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
if [ "$SCOPE" = system ] && [ -z "$RUN_AS" ]; then RUN_AS="$(read_kv "$INSTALL_CONF" RUN_AS)"; fi
if [ "$SCOPE" = system ]; then RUN_AS="${RUN_AS:-maktaba}"; fi
if [ -z "$EXTRAS" ]; then
    if [ "$OS" = darwin ] && [ "$ARCH" = arm64 ]; then EXTRAS=stt; else EXTRAS=none; fi
fi

# --- where the pipeline comes from ---------------------------------------------------------
RELEASE_CONF="$SCRIPT_DIR/release.conf"
# Expected digests: the release's (release.conf), given (--sha256, --constraints-sha256),
# or pinned by the last install (install.conf). Empty: not known yet.
WHEEL_SHA256="$WHEEL_SHA_ARG"
CONSTRAINTS_SHA256="$CONSTRAINTS_SHA_ARG"
PYTHON_VERSION=""       # the Python a wheel's environment gets (uv)

if [ "$ACTION" = uninstall ]; then
    :   # removing needs none of it
elif [ -n "$FROM_WHEEL" ]; then
    FROM=wheel
elif [ -n "$SOURCE" ]; then
    FROM=source
else
    FROM="$(read_kv "$INSTALL_CONF" FROM)"
    case "$FROM" in
        wheel)
            FROM_WHEEL="$(read_kv "$INSTALL_CONF" WHEEL)"
            WHEEL_SHA256="$(read_kv "$INSTALL_CONF" WHEEL_SHA256)"
            if [ -z "$CONSTRAINTS" ]; then
                CONSTRAINTS="$(read_kv "$INSTALL_CONF" CONSTRAINTS)"
                CONSTRAINTS_SHA256="$(read_kv "$INSTALL_CONF" CONSTRAINTS_SHA256)"
            fi
            ;;
        source) SOURCE="$(read_kv "$INSTALL_CONF" SOURCE)" ;;
        release) ;;
        '')
            # an install from before --from-wheel remembered only its source
            SOURCE="$(read_kv "$INSTALL_CONF" SOURCE)"
            if [ -n "$SOURCE" ]; then FROM=source; fi
            ;;
        *) die "$INSTALL_CONF: unknown FROM=$FROM" ;;
    esac
    if [ -z "$FROM" ]; then
        if [ -f "$RELEASE_CONF" ]; then FROM=release; else FROM=source; fi
    fi
fi
if [ "$FROM" = wheel ] && [ -z "$FROM_WHEEL" ]; then
    die "$INSTALL_CONF says FROM=wheel but names no WHEEL: pass --from-wheel"
fi

if [ "$FROM" = release ]; then
    [ -f "$RELEASE_CONF" ] || die "this machine's pipeline came from a release archive, and this script is not from one: run that release's install-node.sh, or pass --from-wheel or --source"
    rel_tag="$(read_kv "$RELEASE_CONF" TAG)"
    rel_repo="$(read_kv "$RELEASE_CONF" REPO)"
    rel_wheel="$(read_kv "$RELEASE_CONF" WHEEL)"
    if [ -z "$rel_tag" ] || [ -z "$rel_repo" ] || [ -z "$rel_wheel" ]; then
        die "$RELEASE_CONF is incomplete (TAG, REPO, WHEEL)"
    fi
    rel_url="https://github.com/$rel_repo/releases/download/$rel_tag"
    # the archive carries the wheel; the release has it too, for an archive without it
    if [ -f "$SCRIPT_DIR/$rel_wheel" ]; then FROM_WHEEL="$SCRIPT_DIR/$rel_wheel"; else FROM_WHEEL="$rel_url/$rel_wheel"; fi
    WHEEL_SHA256="$(read_kv "$RELEASE_CONF" WHEEL_SHA256)"
    if [ -z "$CONSTRAINTS" ]; then
        rel_constraints="$(read_kv "$RELEASE_CONF" CONSTRAINTS)"
        if [ -n "$rel_constraints" ]; then
            if [ -f "$SCRIPT_DIR/$rel_constraints" ]; then
                CONSTRAINTS="$SCRIPT_DIR/$rel_constraints"
            else
                CONSTRAINTS="$rel_url/$rel_constraints"
            fi
            CONSTRAINTS_SHA256="$(read_kv "$RELEASE_CONF" CONSTRAINTS_SHA256)"
        fi
    fi
    PYTHON_VERSION="$(read_kv "$RELEASE_CONF" PYTHON)"
fi
if [ "$FROM" = source ] && [ -z "$SOURCE" ]; then
    SOURCE="$(cd "$SCRIPT_DIR/../../.." && pwd)/pipeline"
fi
if [ "$FROM" = release ] || [ "$FROM" = wheel ]; then
    case "${FROM_WHEEL%%[?#]*}" in
        *.whl) ;;
        *) die "--from-wheel takes a wheel (maktaba_pipeline-<version>-py3-none-any.whl): $(redact_url "$FROM_WHEEL")" ;;
    esac
    case "$(basename "${FROM_WHEEL%%[?#]*}")" in
        maktaba_pipeline-*) ;;
        *) die "$(redact_url "$FROM_WHEEL") is not a maktaba-pipeline wheel (maktaba_pipeline-<version>-...whl)" ;;
    esac
    PYTHON_VERSION="${PYTHON_VERSION:-3.12}"
    # A URL is printed while it downloads and kept in install.conf for updates: one that
    # carries a credential would leave it in logs and on disk.
    for u in "$FROM_WHEEL" "$CONSTRAINTS"; do
        if url_has_credentials "$u"; then
            die "$(redact_url "$u") carries a credential (user:password@ or a ?query such as a token), which would be logged and remembered in install.conf: use a plain URL pinned with --sha256 / --constraints-sha256, or download the file and pass its path"
        fi
    done
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
    # Test hook (dry runs only): take the pip path.
    if [ "${MAKTABA_NODE_NO_UV:-}" = 1 ]; then return 1; fi
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

is_url() { case "$1" in http://*|https://*) return 0 ;; *) return 1 ;; esac; }

abs_path() { printf '%s/%s' "$(cd "$(dirname "$1")" && pwd)" "$(basename "$1")"; }

# file_url PATH: a file:// URL for an absolute PATH (%, space, # and ? escaped).
file_url() {
    printf 'file://%s' "$(printf '%s' "$1" | sed -e 's/%/%25/g' -e 's/ /%20/g' -e 's/#/%23/g' -e 's/?/%3F/g')"
}

# sha256_of FILE: its SHA-256. sha256sum, shasum, else any Python's hashlib; fails only
# when none of them is here.
sha256_of() {
    local p hash_tool="${MAKTABA_NODE_HASH:-}"
    if [ -z "$hash_tool" ] && command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
        return 0
    fi
    if [ -z "$hash_tool" ] && command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
        return 0
    fi
    [ "$hash_tool" = none ] && return 1
    for p in "$PY" "$(find_python 2>/dev/null || true)" "$(command -v python3 2>/dev/null || true)"; do
        if [ -z "$p" ] || [ ! -x "$p" ]; then continue; fi
        "$p" - "$1" <<'PYHASH' && return 0
import hashlib, sys
h = hashlib.sha256()
with open(sys.argv[1], "rb") as f:
    for block in iter(lambda: f.read(1 << 20), b""):
        h.update(block)
print(h.hexdigest())
PYHASH
    done
    return 1
}

# fetch SPEC [SHA256]: FETCHED = a local file for SPEC, a path or an http(s) URL (which
# is downloaded into $NODE_DIR/dist), FETCHED_SHA256 its digest. SHA256, when given,
# must match; a download is always hashed, so the next update can pin it.
FETCHED=""
FETCHED_SHA256=""
fetch() {
    local spec="$1" want="${2:-}" dest got
    FETCHED_SHA256=""
    if is_url "$spec"; then
        dest="$NODE_DIR/dist/$(basename "${spec%%[?#]*}")"
        info "Downloading $(redact_url "$spec")"
        run mkdir -p "$NODE_DIR/dist"
        if command -v curl >/dev/null 2>&1; then
            run curl -fsSL --retry 3 -o "$dest.part" "$spec" || die "could not download $(redact_url "$spec")"
        elif command -v wget >/dev/null 2>&1; then
            run wget -q -O "$dest.part" "$spec" || die "could not download $(redact_url "$spec")"
        else
            die "neither curl nor wget found to download $(redact_url "$spec"): download it and pass the file"
        fi
        run mv -f "$dest.part" "$dest"
    else
        [ -f "$spec" ] || die "no such file: $spec"
        dest="$(abs_path "$spec")"
    fi
    FETCHED="$dest"
    # (a dry run downloads nothing: nothing to hash then)
    if [ -f "$dest" ] && { [ -n "$want" ] || is_url "$spec"; }; then
        got="$(sha256_of "$dest")" \
            || die "cannot check $dest: no sha256sum, shasum or Python here to compute its SHA-256"
        if [ -n "$want" ] && [ "$got" != "$want" ]; then
            if is_url "$spec"; then rm -f -- "$dest"; fi   # not left for anything to use
            die "$dest: SHA-256 $got, expected $want (the release's, --sha256's, or the one pinned at the first install; pass --from-wheel again to take a new file)"
        fi
        FETCHED_SHA256="$got"
    fi
}

# prune_dist KEEP...: remove the earlier downloads in $NODE_DIR/dist, keeping KEEP.
prune_dist() {
    local f k keep
    [ -d "$NODE_DIR/dist" ] || return 0
    for f in "$NODE_DIR"/dist/*; do
        [ -e "$f" ] || continue
        keep=0
        for k in "$@"; do if [ "$f" = "$k" ]; then keep=1; fi; done
        if [ "$keep" = 0 ]; then remove "$f"; fi
    done
}

# extras_suffix: "[a,b]" for the chosen extras, or nothing.
extras_suffix() { if [ "$EXTRAS" != none ]; then printf '[%s]' "$EXTRAS"; fi; }

build_venv() {
    if [ "$FROM" = source ]; then build_venv_source; else build_venv_wheel; fi
}

# The digests install.conf pins for FROM=wheel (see install_helpers).
WHEEL_PIN=""
CONSTRAINTS_PIN=""

# in_dir DIR CMD...: run CMD from DIR.
in_dir() {
    local dir="$1"
    shift
    (
        if [ "$DRY_RUN" = 1 ]; then printf '  + cd %s\n' "$(quote_cmd "$dir")"; else cd "$dir"; fi
        run "$@"
    )
}

# The release's (or a given) wheel, its dependencies pinned by the constraints file.
build_venv_wheel() {
    local uv py wheel constraints="" cons_args=() spec here
    run mkdir -p "$NODE_DIR"
    fetch "$FROM_WHEEL" "$WHEEL_SHA256"
    wheel="$FETCHED"
    WHEEL_PIN="${WHEEL_SHA256:-$FETCHED_SHA256}"
    if [ -n "$CONSTRAINTS" ]; then
        fetch "$CONSTRAINTS" "$CONSTRAINTS_SHA256"
        constraints="$FETCHED"
        CONSTRAINTS_PIN="${CONSTRAINTS_SHA256:-$FETCHED_SHA256}"
    else
        warn "no constraints file: the wheel's dependencies are not pinned (pass --constraints)"
    fi
    spec="$wheel$(extras_suffix)"
    # The installs run from the constraints file's directory and name it ./<file>: uv
    # splits a --constraint path at its spaces (~/Library/Application Support/...).
    here="$NODE_DIR"
    local install_args=("$spec") req="" wheel_hash
    if [ -n "$constraints" ]; then
        here="$(dirname "$constraints")"
        cons_args=(--constraint "./$(basename "$constraints")")
        # Hash-checking mode: every dependency must match a --hash of the constraints file
        # (build-release.sh exports them from uv.lock), and the wheel its own SHA-256,
        # through a one-line requirements file next to the constraints.
        if [ -f "$constraints" ] && ! grep -q -- '--hash=sha256:' "$constraints"; then
            die "$constraints pins no hashes (--hash=sha256:...), so the dependencies downloaded from PyPI could not be checked: use the constraints file of a release made with hashes"
        fi
        wheel_hash="$WHEEL_PIN"
        if [ -z "$wheel_hash" ] && [ -f "$wheel" ]; then
            wheel_hash="$(sha256_of "$wheel")" \
                || die "cannot check $wheel: no sha256sum, shasum or Python here to compute its SHA-256"
        fi
        # (a dry run downloads nothing: no digest to write then)
        wheel_hash="${wheel_hash:-0000000000000000000000000000000000000000000000000000000000000000}"
        req="$here/.maktaba-node-requirements.txt"
        printf 'maktaba-pipeline%s @ %s --hash=sha256:%s\n' "$(extras_suffix)" "$(file_url "$wheel")" "$wheel_hash" \
            | write_file "$req" 0644
        install_args=(--require-hashes ${cons_args[@]+"${cons_args[@]}"} -r "./$(basename "$req")")
    fi
    if uv="$(find_uv)"; then
        info "Installing $(basename "$wheel") with uv ($VENV)"
        local uv_env=()
        if [ "$SCOPE" = system ]; then
            # a uv-managed Python must sit where the service account can read it
            uv_env+=("UV_PYTHON_INSTALL_DIR=$NODE_DIR/python")
        fi
        if [ ! -x "$PY" ]; then
            run env ${uv_env[@]+"${uv_env[@]}"} "$uv" venv --python "$PYTHON_VERSION" "$VENV"
        fi
        # --reinstall-package: a rebuilt wheel of an unchanged version replaces the old one
        in_dir "$here" env ${uv_env[@]+"${uv_env[@]}"} "$uv" pip install --python "$PY" \
            --reinstall-package maktaba-pipeline "${install_args[@]}"
    elif py="$(find_python)"; then
        warn "uv not found: using $py and pip."
        if [ ! -x "$PY" ]; then run "$py" -m venv "$VENV"; fi
        if [ -n "$req" ]; then
            # A venv's own pip (no unchecked upgrade from PyPI) does not take hashes from a
            # constraints file: install its pins as requirements, each checked, then the
            # wheel, checked, without resolving again (every extra's pins come along).
            in_dir "$here" "$PY" -m pip install --quiet --require-hashes --no-deps -r "./$(basename "$constraints")"
            in_dir "$here" "$PY" -m pip install --quiet --require-hashes --no-deps --force-reinstall -r "./$(basename "$req")"
        else
            run "$PY" -m pip install --quiet --upgrade pip
            in_dir "$here" "$PY" -m pip install --quiet "$spec"
            run "$PY" -m pip install --quiet --force-reinstall --no-deps "$wheel"
        fi
    else
        die "neither uv nor Python 3.12+ found. Install uv (https://docs.astral.sh/uv/) and run this again."
    fi
    if [ -n "$req" ]; then remove "$req"; fi
    # installed: earlier updates' downloads are of no more use
    prune_dist "$wheel" ${constraints:+"$constraints"}
}

# A checkout's pipeline/, its dependencies pinned by its uv.lock.
build_venv_source() {
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
    # FROM=release re-reads release.conf on the next run, so a newer release's archive
    # installs its own wheel; FROM=wheel keeps the wheel (and constraints) given.
    local wheel_kv="" cons_kv="" src_kv="" wheel_sha_kv="" cons_sha_kv=""
    if [ "$FROM" = source ]; then src_kv="$SOURCE"; fi
    if [ "$FROM" = wheel ]; then
        wheel_kv="$FROM_WHEEL"
        if ! is_url "$wheel_kv"; then wheel_kv="$(abs_path "$wheel_kv")"; fi
        cons_kv="$CONSTRAINTS"
        if [ -n "$cons_kv" ] && ! is_url "$cons_kv"; then cons_kv="$(abs_path "$cons_kv")"; fi
        # A download is pinned to what this install got (trust on first use) unless a
        # digest was given; a local file only to a given digest, so a rebuilt wheel at
        # the same path still installs.
        wheel_sha_kv="$WHEEL_SHA256"
        if is_url "$FROM_WHEEL"; then wheel_sha_kv="$WHEEL_PIN"; fi
        cons_sha_kv="$CONSTRAINTS_SHA256"
        if [ -n "$CONSTRAINTS" ] && is_url "$CONSTRAINTS"; then cons_sha_kv="$CONSTRAINTS_PIN"; fi
    fi
    write_file "$INSTALL_CONF" 0644 <<EOF
# install-node.sh remembers these for updates
FROM=$FROM
EXTRAS=$EXTRAS
SOURCE=$src_kv
WHEEL=$wheel_kv
WHEEL_SHA256=$wheel_sha_kv
CONSTRAINTS=$cons_kv
CONSTRAINTS_SHA256=$cons_sha_kv
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
        *) die "the pipeline from $(pipeline_origin) has no 'run --node': install a newer one and run this again" ;;
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

pipeline_origin() {
    case "$FROM" in
        source)  printf '%s' "$SOURCE" ;;
        release) printf 'release %s (%s)' "$(read_kv "$RELEASE_CONF" TAG)" "$FROM_WHEEL" ;;
        *)       printf '%s' "$FROM_WHEEL" ;;
    esac
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
    echo "  Pipeline:     $(pipeline_origin)"
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
    if [ "$FROM" = source ]; then
        [ -f "$SOURCE/pyproject.toml" ] \
            || die "no pipeline source at $SOURCE (pass --source DIR, or --from-wheel URL|PATH)"
        grep -q '^name = "maktaba-pipeline"' "$SOURCE/pyproject.toml" \
            || die "$SOURCE is not the maktaba-pipeline project"
    fi
    if [ "$OS" = linux ]; then check_systemd; fi
    plan_instances
    if [ "${#WANTED[@]}" -gt 0 ] && ! { [ "$OS" = darwin ] && [ "$ARCH" = arm64 ]; }; then
        warn "transcription runs on Apple Silicon only today: the extra workers claim nothing"
        warn "here unless node@<N>.env gives them other stages (MAKTABA_PIPELINE_STAGES)."
    fi

    info "Maktaba node: $OS/$ARCH, ${SCOPE} scope${RUN_AS:+, runs as $RUN_AS}, extras: $EXTRAS, extra workers: ${#WANTED[@]}"
    info "Pipeline: $(pipeline_origin)"
    ensure_run_as
    build_venv
    install_helpers
    check_node_mode
    ensure_env_file
    join_if_needed
    if [ "$FULL" = 1 ]; then
        convert_full
        return
    fi
    if [ "$OS" = darwin ]; then install_launchd; else install_systemd; fi
    summary
}

# convert_full: Epic 35 story 35.8 AC7 — `maktaba-server setup --node` makes this worker a
# full node: the home's release, verified in process, side by side; node.json and this
# worker's Python environment kept; the old worker units removed once the node runs.
convert_full() {
    local server="" args=()
    if [ -n "$SERVER_DIR" ]; then
        [ -d "$SERVER_DIR" ] || die "--server-dir $SERVER_DIR is not a directory"
        if [ -x "$SERVER_DIR/maktaba-server" ]; then server="$SERVER_DIR/maktaba-server"; fi
        args+=(--from "$SERVER_DIR")
    fi
    if [ -z "$server" ]; then server="$(command -v maktaba-server || true)"; fi
    if [ -z "$server" ]; then
        if [ "$DRY_RUN" = 1 ]; then server=maktaba-server; else
            die "--full needs maktaba-server: give --server-dir DIR (the release, extracted) or put it on PATH"
        fi
    fi
    args+=(--python "$PY")
    if [ -n "$INSTANCES" ]; then args+=(--instances "$INSTANCES"); fi
    info "Converting this worker into a full node (maktaba-server setup --node)"
    run "$server" setup --node "${args[@]}"
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
