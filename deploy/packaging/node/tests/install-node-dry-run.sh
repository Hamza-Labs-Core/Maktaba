#!/usr/bin/env bash
# Dry-run tests for install-node.sh: where the pipeline comes from (--from-wheel, a
# release archive's release.conf, --source / the checkout, the last install's choice)
# and the commands that build the worker's environment from it. Nothing is installed:
# every run is --dry-run with HOME in a temporary directory.
#
# Usage: deploy/packaging/node/tests/install-node-dry-run.sh
set -euo pipefail

NODE_SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_PIPELINE="$(cd "$NODE_SRC/../../.." && pwd)/pipeline"
T="$(mktemp -d "${TMPDIR:-/tmp}/install-node-test.XXXXXX")"
T="$(cd "$T" && pwd -P)"   # the installer prints normalised paths
trap 'rm -rf "$T"' EXIT

PASS=0
FAIL=0
OUT=""
STATUS=0

# run_installer DIR [ENV=VALUE...] -- ARGS...: install-node.sh from DIR as a fresh Linux
# user (HOME=$T/home) unless the case set HOME; OUT gets stdout+stderr, STATUS the exit.
run_installer() {
    local dir="$1" envs=()
    shift
    while [ $# -gt 0 ] && [ "$1" != -- ]; do envs+=("$1"); shift; done
    shift
    set +e
    OUT="$(env HOME="$T/home" MAKTABA_NODE_OS=linux ${envs[@]+"${envs[@]}"} \
        "$dir/install-node.sh" --dry-run --no-join --no-start --no-linger "$@" 2>&1)"
    STATUS=$?
    set -e
}

ok()   { PASS=$((PASS + 1)); printf 'ok    %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf 'FAIL  %s\n' "$1"; printf '%s\n' "$OUT" | sed 's/^/      | /'; }

expect_ok() { if [ "$STATUS" = 0 ]; then ok "$1: exits 0"; else fail "$1: exit $STATUS"; fi; }
expect_err() {
    if [ "$STATUS" != 0 ] && grep -qF -- "$2" <<<"$OUT"; then ok "$1: refused ($2)"; else fail "$1: expected an error with '$2'"; fi
}
has()   { if grep -qF -- "$2" <<<"$OUT"; then ok "$1"; else fail "$1: no '$2'"; fi; }
lacks() { if grep -qF -- "$2" <<<"$OUT"; then fail "$1: unexpected '$2'"; else ok "$1"; fi; }
# a dry-run command line, "+ ..." with all of the given words
has_cmd() {
    local what="$1" line found=0 w all
    shift
    while IFS= read -r line; do
        case "$line" in '  + '*) ;; *) continue ;; esac
        all=1
        for w in "$@"; do case "$line" in *"$w"*) ;; *) all=0 ;; esac; done
        if [ "$all" = 1 ]; then found=1; fi
    done <<<"$OUT"
    if [ "$found" = 1 ]; then ok "$what"; else fail "$what: no command with: $*"; fi
}
# the rendered install.conf of the last dry run
rendered_conf() {
    local root
    root="$(sed -n 's/.*Rendered files are under //p' <<<"$OUT" | tail -n 1)"
    cat "$root$T/home/.local/share/maktaba/node/install.conf"
}

sha256() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
    else shasum -a 256 "$1" | awk '{print $1}'; fi
}

# --- fixtures: a wheel, constraints, and a release archive's directory ------------------------
mkdir -p "$T/home" "$T/files"
WHEEL="$T/files/maktaba_pipeline-9.8.7-py3-none-any.whl"
CONS="$T/files/maktaba-pipeline-9.8.7-constraints.txt"
printf 'not really a wheel\n' >"$WHEEL"
printf 'structlog==25.1.0\n' >"$CONS"

# make_bundle DIR [with-files]: the installer as a maktaba-node-9.8.7 archive unpacks it
make_bundle() {
    local dir="$1"
    mkdir -p "$dir"
    (cd "$NODE_SRC" && tar --exclude=./tests -cf - .) | (cd "$dir" && tar -xf -)
    if [ "${2:-}" = with-files ]; then cp "$WHEEL" "$CONS" "$dir/"; fi
    cat >"$dir/release.conf" <<EOF
TAG=v9.8.7
VERSION=9.8.7
REPO=example/Maktaba
WHEEL=$(basename "$WHEEL")
WHEEL_SHA256=$(sha256 "$WHEEL")
CONSTRAINTS=$(basename "$CONS")
CONSTRAINTS_SHA256=$(sha256 "$CONS")
PYTHON=3.13
EOF
}
BUNDLE="$T/bundle"
make_bundle "$BUNDLE" with-files
BARE_BUNDLE="$T/bare-bundle"
make_bundle "$BARE_BUNDLE"

# --- release archive: its own wheel, checked, pinned -------------------------------------------
run_installer "$BUNDLE" --
expect_ok "release archive"
has "release archive: says which release" "Pipeline: release v9.8.7"
has_cmd "release archive: venv with the release's Python" "uv venv --python 3.13"
has_cmd "release archive: installs the bundled wheel, constrained" \
    "pip install" "--constraint $BUNDLE/$(basename "$CONS")" "$BUNDLE/$(basename "$WHEEL")"
lacks "release archive: no download" "curl"
lacks "release archive: no uv sync" "uv sync"
has "release archive: remembers FROM=release" "Pipeline: release"
conf="$(rendered_conf)"
if grep -qx 'FROM=release' <<<"$conf" && grep -qx 'WHEEL=' <<<"$conf"; then
    ok "release archive: install.conf has FROM=release and no wheel"
else
    OUT="$conf"; fail "release archive: install.conf"
fi

run_installer "$BUNDLE" -- --extras stt,search
# (printed shell-quoted: maktaba_pipeline-...whl\[stt\,search\])
has_cmd "release archive: extras on the wheel" "pip install" "$(basename "$WHEEL")" "[stt" "search"

run_installer "$BUNDLE" MAKTABA_NODE_OS=linux -- --system
has_cmd "release archive, --system: uv's Python under the node dir" \
    "UV_PYTHON_INSTALL_DIR=/opt/maktaba/node/python" "uv venv"

# a wheel that does not match the release's digest is refused
TAMPERED="$T/tampered"
make_bundle "$TAMPERED" with-files
printf 'tampered\n' >>"$TAMPERED/$(basename "$WHEEL")"
run_installer "$TAMPERED" --
expect_err "release archive: tampered wheel" "SHA-256"

# --- release archive without the files: from the GitHub release ---------------------------------
run_installer "$BARE_BUNDLE" --
expect_ok "bare release archive"
has_cmd "bare release archive: downloads the wheel from the release" \
    "curl" "https://github.com/example/Maktaba/releases/download/v9.8.7/$(basename "$WHEEL")"
has_cmd "bare release archive: downloads the constraints" \
    "curl" "https://github.com/example/Maktaba/releases/download/v9.8.7/$(basename "$CONS")"
has_cmd "bare release archive: installs the download" \
    "pip install" "$T/home/.local/share/maktaba/node/dist/$(basename "$WHEEL")"

# --- --from-wheel PATH / URL ----------------------------------------------------------------------
run_installer "$NODE_SRC" -- --from-wheel "$WHEEL"
expect_ok "--from-wheel PATH"
has_cmd "--from-wheel PATH: installs it" "pip install" "$WHEEL"
has "--from-wheel PATH: warns that nothing pins the dependencies" "not pinned"
has_cmd "--from-wheel PATH: Python 3.12 by default" "uv venv --python 3.12"
lacks "--from-wheel PATH: not the checkout" "uv sync"
conf="$(rendered_conf)"
if grep -qx 'FROM=wheel' <<<"$conf" && grep -qx "WHEEL=$WHEEL" <<<"$conf"; then
    ok "--from-wheel PATH: install.conf remembers the wheel"
else
    OUT="$conf"; fail "--from-wheel PATH: install.conf"
fi

(cd "$T/files" && run_installer "$NODE_SRC" -- --from-wheel "./$(basename "$WHEEL")" \
    --constraints "./$(basename "$CONS")"
    has_cmd "--from-wheel ./relative: made absolute" "--constraint $CONS" "$WHEEL")

run_installer "$NODE_SRC" -- --from-wheel "https://example.com/dl/maktaba_pipeline-1.0.0-py3-none-any.whl?x=1" \
    --constraints https://example.com/dl/c.txt
expect_ok "--from-wheel URL"
has_cmd "--from-wheel URL: downloads it" "curl" "-o $T/home/.local/share/maktaba/node/dist/maktaba_pipeline-1.0.0-py3-none-any.whl.part"
has_cmd "--from-wheel URL: installs the download, constrained" \
    "pip install" "--constraint $T/home/.local/share/maktaba/node/dist/c.txt" \
    "$T/home/.local/share/maktaba/node/dist/maktaba_pipeline-1.0.0-py3-none-any.whl"

run_installer "$BUNDLE" -- --from-wheel "$WHEEL"
has_cmd "--from-wheel beats release.conf" "pip install" "$WHEEL"
lacks "--from-wheel beats release.conf: not the release's" "Pipeline: release"

# --- pip when there is no uv ----------------------------------------------------------------------
if command -v python3.12 >/dev/null 2>&1 || command -v python3.13 >/dev/null 2>&1 \
    || python3 -c 'import sys; sys.exit(sys.version_info < (3, 12))' 2>/dev/null; then
    run_installer "$BUNDLE" MAKTABA_NODE_NO_UV=1 --
    expect_ok "pip"
    has_cmd "pip: constrained install of the wheel" "-m pip install" "--constraint" "$(basename "$WHEEL")"
    has_cmd "pip: then the wheel itself again" "--force-reinstall --no-deps" "$(basename "$WHEEL")"
    lacks "pip: no uv" "uv venv"
else
    printf 'skip  pip: no Python 3.12+ here\n'
fi

# --- refusals --------------------------------------------------------------------------------------
run_installer "$NODE_SRC" -- --from-wheel "$WHEEL" --source "$REPO_PIPELINE"
expect_err "--from-wheel with --source" "give one"
run_installer "$NODE_SRC" -- --source "$REPO_PIPELINE" --constraints "$CONS"
expect_err "--constraints with --source" "--constraints goes with --from-wheel"
run_installer "$NODE_SRC" -- --from-wheel "$CONS"
expect_err "--from-wheel of a non-wheel" "takes a wheel"
cp "$WHEEL" "$T/files/other_pkg-1.0-py3-none-any.whl"
run_installer "$NODE_SRC" -- --from-wheel "$T/files/other_pkg-1.0-py3-none-any.whl"
expect_err "--from-wheel of another package" "not a maktaba-pipeline wheel"
run_installer "$NODE_SRC" -- --from-wheel "$T/files/maktaba_pipeline-0-py3-none-any.whl"
expect_err "--from-wheel of a missing file" "no such file"
if MAKTABA_NODE_NO_UV=1 "$NODE_SRC/install-node.sh" --help >/dev/null 2>&1 \
    && ! (HOME="$T/home" MAKTABA_NODE_NO_UV=1 "$NODE_SRC/install-node.sh" --no-join >/dev/null 2>&1); then
    ok "MAKTABA_NODE_NO_UV: dry runs only"
else
    fail "MAKTABA_NODE_NO_UV: dry runs only"
fi

# --- the checkout (the fallback) ---------------------------------------------------------------------
run_installer "$NODE_SRC" --
expect_ok "checkout"
has_cmd "checkout: uv sync of pipeline/" "uv sync --project $REPO_PIPELINE" "--frozen"
lacks "checkout: no wheel" ".whl"

# --- the last install's choice -------------------------------------------------------------------------
remember() {
    mkdir -p "$1/.local/share/maktaba/node"
    cat >"$1/.local/share/maktaba/node/install.conf"
}
remember "$T/h-wheel" <<EOF
FROM=wheel
EXTRAS=none
WHEEL=$WHEEL
CONSTRAINTS=$CONS
EOF
run_installer "$BUNDLE" HOME="$T/h-wheel" --
has_cmd "update: a remembered wheel beats release.conf" "--constraint $CONS" "$WHEEL"

remember "$T/h-old" <<EOF
EXTRAS=none
SOURCE=$REPO_PIPELINE
EOF
run_installer "$BUNDLE" HOME="$T/h-old" --
has_cmd "update: an install from before --from-wheel keeps its source" "uv sync --project $REPO_PIPELINE"

remember "$T/h-rel" <<EOF
FROM=release
EXTRAS=none
EOF
run_installer "$BUNDLE" HOME="$T/h-rel" --
has_cmd "update: FROM=release takes this archive's release" "pip install" "$BUNDLE/$(basename "$WHEEL")"
run_installer "$NODE_SRC" HOME="$T/h-rel" --
expect_err "update: FROM=release from a checkout" "not from one"
set +e
OUT="$(HOME="$T/h-rel" MAKTABA_NODE_OS=linux "$NODE_SRC/uninstall-node.sh" --dry-run 2>&1)"
STATUS=$?
set -e
expect_ok "uninstall: a release install, from a checkout"
has "uninstall: removes the node dir" "rm -rf -- $T/h-rel/.local/share/maktaba/node"
run_installer "$NODE_SRC" HOME="$T/h-rel" -- --source "$REPO_PIPELINE"
has_cmd "update: --source switches a release install to the checkout" "uv sync --project $REPO_PIPELINE"

echo
printf '%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
