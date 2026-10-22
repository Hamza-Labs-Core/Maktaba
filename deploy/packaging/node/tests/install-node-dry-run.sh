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
# the rendered one-line requirements file (the wheel and its hash) of the last dry run,
# written next to the constraints in DIR
rendered_req() {
    local root
    root="$(sed -n 's/.*Rendered files are under //p' <<<"$OUT" | tail -n 1)"
    cat "$root$1/.maktaba-node-requirements.txt" 2>/dev/null || true
}
# req_has WHAT DIR TEXT: that file has TEXT
req_has() {
    local req
    req="$(rendered_req "$2")"
    if grep -qF -- "$3" <<<"$req"; then ok "$1"; else OUT="$OUT
[requirements file: $req]"; fail "$1: no '$3' in the requirements file"; fi
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
printf 'structlog==25.1.0 \\\n    --hash=sha256:%s\n' "$(printf 'a%.0s' $(seq 64))" >"$CONS"
NOHASH_CONS="$T/files/nohash-constraints.txt"
printf 'structlog==25.1.0\n' >"$NOHASH_CONS"

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
has_cmd "release archive: installs in hash-checking mode, constrained" \
    "pip install" "--require-hashes" "--constraint ./$(basename "$CONS")" "-r ./.maktaba-node-requirements.txt"
has_cmd "release archive: each pin looked for on every index the constraints name" \
    "uv pip install" "--require-hashes --index-strategy unsafe-best-match"
req_has "release archive: the bundled wheel, by its SHA-256" "$BUNDLE" \
    "maktaba-pipeline @ file://$BUNDLE/$(basename "$WHEEL") --hash=sha256:$(sha256 "$WHEEL")"
has_cmd "release archive: from the constraints' directory (uv splits a --constraint path at spaces)" \
    "cd $BUNDLE"
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
req_has "release archive: extras on the wheel" "$BUNDLE" "maktaba-pipeline[stt,search] @ file://$BUNDLE/$(basename "$WHEEL")"

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
req_has "bare release archive: installs the download" "$T/home/.local/share/maktaba/node/dist" \
    "@ file://$T/home/.local/share/maktaba/node/dist/$(basename "$WHEEL") --hash=sha256:"

# --- the stt and search extras: their pins with pip ------------------------------------------------
# (PyTorch's index, which the constraints name, also matches their torch pins with other
# files: pip takes the file each pin's hashes name, whichever index has it)
for extras in stt search,mediaintel; do
    run_installer "$BUNDLE" -- --extras "$extras"
    expect_ok "--extras $extras"
    has_cmd "--extras $extras: a venv with the release's Python" "uv venv --python 3.13"
    has_cmd "--extras $extras: the venv's own pip" "-m ensurepip --default-pip"
    has_cmd "--extras $extras: the pins, hash-checked, with pip" \
        "-m pip install" "--require-hashes --no-deps -r ./$(basename "$CONS")"
    has_cmd "--extras $extras: then the wheel, hash-checked" \
        "-m pip install" "--require-hashes --no-deps --force-reinstall -r ./.maktaba-node-requirements.txt"
    lacks "--extras $extras: uv installs nothing" "uv pip install"
done
req_has "--extras: the wheel with them" "$BUNDLE" "maktaba-pipeline[search,mediaintel] @ file://"

# --- an archive unpacked under a path with a space -----------------------------------------------
SPACED="$T/My Downloads/maktaba-node-9.8.7"
make_bundle "$SPACED" with-files
run_installer "$SPACED" --
expect_ok "archive under a path with a space"
has_cmd "archive under a path with a space: cd there, constraints relative" "cd $T/My\\ Downloads/maktaba-node-9.8.7"
has_cmd "archive under a path with a space: --constraint ./<file>" "--constraint ./$(basename "$CONS")"
req_has "archive under a path with a space: the wheel's file URL escapes it" "$SPACED" \
    "file://$T/My%20Downloads/maktaba-node-9.8.7/$(basename "$WHEEL")"

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
    has_cmd "--from-wheel ./relative: constrained" "--constraint ./$(basename "$CONS")"
    req_has "--from-wheel ./relative: made absolute" "$T/files" "file://$WHEEL")

run_installer "$NODE_SRC" -- --from-wheel "https://example.com/dl/maktaba_pipeline-1.0.0-py3-none-any.whl" \
    --constraints https://example.com/dl/c.txt
expect_ok "--from-wheel URL"
has_cmd "--from-wheel URL: downloads it" "curl" "-o $T/home/.local/share/maktaba/node/dist/maktaba_pipeline-1.0.0-py3-none-any.whl.part"
has_cmd "--from-wheel URL: installs the download, constrained, hash-checked" \
    "pip install" "--require-hashes" "--constraint ./c.txt"
req_has "--from-wheel URL: the download" "$T/home/.local/share/maktaba/node/dist" \
    "file://$T/home/.local/share/maktaba/node/dist/maktaba_pipeline-1.0.0-py3-none-any.whl"

# --- a URL with a credential is neither logged nor remembered ---------------------------------
run_installer "$NODE_SRC" -- --from-wheel "https://example.com/dl/maktaba_pipeline-1.0.0-py3-none-any.whl?token=s3cr3t-tok"
expect_err "--from-wheel URL with a ?token" "carries a credential"
lacks "--from-wheel URL with a ?token: not in the output" "s3cr3t-tok"
has "--from-wheel URL with a ?token: shown redacted" "https://example.com/dl/maktaba_pipeline-1.0.0-py3-none-any.whl?***"
run_installer "$NODE_SRC" -- --from-wheel "https://bob:hunter2pw@example.com/dl/maktaba_pipeline-1.0.0-py3-none-any.whl"
expect_err "--from-wheel URL with user:password@" "carries a credential"
lacks "--from-wheel URL with user:password@: not in the output" "hunter2pw"
run_installer "$NODE_SRC" -- --from-wheel "$WHEEL" --constraints "https://example.com/c.txt?X-Amz-Signature=abcsig123"
expect_err "--constraints URL with a signature" "carries a credential"
lacks "--constraints URL with a signature: not in the output" "abcsig123"
mkdir -p "$T/h-tok/.local/share/maktaba/node"
cat >"$T/h-tok/.local/share/maktaba/node/install.conf" <<EOF
FROM=wheel
EXTRAS=none
WHEEL=https://example.com/maktaba_pipeline-1.0.0-py3-none-any.whl?token=old-s3cr3t
EOF
run_installer "$NODE_SRC" HOME="$T/h-tok" --
expect_err "update: a remembered URL with a token is refused" "carries a credential"
lacks "update: a remembered URL with a token: not in the output" "old-s3cr3t"

# --- constraints without hashes are refused ------------------------------------------------------
run_installer "$NODE_SRC" -- --from-wheel "$WHEEL" --constraints "$NOHASH_CONS"
expect_err "--constraints without hashes" "pins no hashes"

run_installer "$BUNDLE" -- --from-wheel "$WHEEL"
has_cmd "--from-wheel beats release.conf" "pip install" "$WHEEL"
lacks "--from-wheel beats release.conf: not the release's" "Pipeline: release"

# --- pip when there is no uv ----------------------------------------------------------------------
if command -v python3.12 >/dev/null 2>&1 || command -v python3.13 >/dev/null 2>&1 \
    || python3 -c 'import sys; sys.exit(sys.version_info < (3, 12))' 2>/dev/null; then
    run_installer "$BUNDLE" MAKTABA_NODE_NO_UV=1 --
    expect_ok "pip"
    has_cmd "pip: the constraints' pins, hash-checked" "-m pip install" "--require-hashes --no-deps -r ./$(basename "$CONS")"
    has_cmd "pip: then the wheel, hash-checked" "--require-hashes --no-deps --force-reinstall -r ./.maktaba-node-requirements.txt"
    lacks "pip: no unchecked pip upgrade" "--upgrade pip"
    lacks "pip: no uv" "uv venv"
    has_cmd "pip: a venv of the Python found" "-m venv $T/home/.local/share/maktaba/node/venv"
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
has_cmd "update: a remembered wheel beats release.conf" "--constraint ./$(basename "$CONS")"
req_has "update: a remembered wheel beats release.conf (the wheel)" "$T/files" "file://$WHEEL"

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
req_has "update: FROM=release takes this archive's release" "$BUNDLE" "file://$BUNDLE/$(basename "$WHEEL")"
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

# --- digests: never skipped ------------------------------------------------------------------------
run_installer "$BUNDLE" MAKTABA_NODE_HASH=python --
expect_ok "no sha256sum/shasum: Python hashes the release's wheel"
run_installer "$TAMPERED" MAKTABA_NODE_HASH=python --
expect_err "no sha256sum/shasum: a tampered wheel is still refused" "SHA-256"
run_installer "$BUNDLE" MAKTABA_NODE_HASH=none --
expect_err "no way to hash: the release's wheel is not installed unchecked" "cannot check"

WHEEL_SHA="$(sha256 "$WHEEL")"
run_installer "$NODE_SRC" -- --from-wheel "$WHEEL" --sha256 "$(printf '%s' "$WHEEL_SHA" | tr 'a-f' 'A-F')"
expect_ok "--sha256: the right digest (any case)"
conf="$(rendered_conf)"
if grep -qx "WHEEL_SHA256=$WHEEL_SHA" <<<"$conf"; then
    ok "--sha256: remembered in install.conf"
else
    OUT="$conf"; fail "--sha256: remembered in install.conf"
fi
run_installer "$NODE_SRC" -- --from-wheel "$WHEEL" --sha256 "$(printf '0%.0s' $(seq 64))"
expect_err "--sha256: a wrong digest" "expected 0000"
run_installer "$NODE_SRC" -- --from-wheel "$WHEEL" --sha256 abc
expect_err "--sha256: not a digest" "64 characters"
run_installer "$BUNDLE" -- --sha256 "$WHEEL_SHA"
expect_err "--sha256 without --from-wheel" "--sha256 goes with --from-wheel"
run_installer "$NODE_SRC" -- --from-wheel "$WHEEL" --constraints-sha256 "$WHEEL_SHA"
expect_err "--constraints-sha256 without --constraints" "goes with --constraints"
run_installer "$NODE_SRC" -- --from-wheel "$WHEEL" --constraints "$CONS" --constraints-sha256 "$WHEEL_SHA"
expect_err "--constraints-sha256: a wrong digest" "expected $WHEEL_SHA"

run_installer "$NODE_SRC" -- --from-wheel "$WHEEL"
conf="$(rendered_conf)"
if grep -qx 'WHEEL_SHA256=' <<<"$conf"; then
    ok "a local wheel without --sha256 is not pinned (a rebuild at the same path installs)"
else
    OUT="$conf"; fail "a local wheel without --sha256 is not pinned"
fi

# an update whose remembered (pinned) digest no longer matches the file
remember "$T/h-pinned" <<EOF
FROM=wheel
EXTRAS=none
WHEEL=$WHEEL
WHEEL_SHA256=$(printf '1%.0s' $(seq 64))
EOF
run_installer "$NODE_SRC" HOME="$T/h-pinned" --
expect_err "update: a wheel that changed since it was pinned" "pinned at the first install"
run_installer "$NODE_SRC" HOME="$T/h-pinned" -- --from-wheel "$WHEEL"
expect_ok "update: --from-wheel again takes the new file"

# --- earlier downloads are removed ---------------------------------------------------------------------
mkdir -p "$T/h-dist/.local/share/maktaba/node/dist"
touch "$T/h-dist/.local/share/maktaba/node/dist/maktaba_pipeline-1.0.0-py3-none-any.whl" \
    "$T/h-dist/.local/share/maktaba/node/dist/maktaba-pipeline-1.0.0-constraints.txt"
run_installer "$BUNDLE" HOME="$T/h-dist" --
has_cmd "an update removes the last release's downloaded wheel" \
    "rm -rf -- $T/h-dist/.local/share/maktaba/node/dist/maktaba_pipeline-1.0.0-py3-none-any.whl"
has_cmd "an update removes the last release's downloaded constraints" \
    "rm -rf -- $T/h-dist/.local/share/maktaba/node/dist/maktaba-pipeline-1.0.0-constraints.txt"
# --- --full: a worker becomes a full node (Epic 35 story 35.8 TC6) --------------------------------
mkdir -p "$T/full-release"
run_installer "$NODE_SRC" HOME="$T/h-full" -- --full --server-dir "$T/full-release" --instances 2
expect_ok "--full"
has_cmd "--full runs setup --node with the release, the worker's Python and its instances" \
    "maktaba-server setup --node --from $T/full-release --python $T/h-full/.local/share/maktaba/node/venv/bin/python --instances 2"
if grep -q "systemctl --user enable" <<<"$OUT"; then
    fail "--full registers no worker unit (setup --node registers maktaba-server)"
else
    ok "--full registers no worker unit"
fi
echo
printf '%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
