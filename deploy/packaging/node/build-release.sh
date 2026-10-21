#!/usr/bin/env bash
# Builds the node's release assets (Epic 31): what a machine needs to join a Maktaba
# home without a git checkout.
#
#   maktaba_pipeline-<ver>-py3-none-any.whl     the pipeline, version-stamped
#   maktaba-pipeline-<ver>-constraints.txt      its dependencies as uv.lock pins them
#                                               (every extra but dev), with the package
#                                               indexes they come from
#   maktaba-node-<ver>.tar.gz / .zip            the node installer (this directory) with
#                                               the wheel, the constraints and
#                                               release.conf, which makes the installer
#                                               default to that wheel
#
# The release workflow runs it for a tag; CI runs it to smoke-test the wheel.
#
# Usage: build-release.sh --version VERSION [--out DIR] [--repo OWNER/REPO]
#   --version  the release tag or version (v1.2.3 or 1.2.3; PEP 440 after the "v")
#   --out      where the assets go (default: ./dist)
#   --repo     the GitHub repository the release is published in (default:
#              $GITHUB_REPOSITORY, else Hamza-Labs-Core/Maktaba); the installer
#              downloads the wheel from there when the bundle has none
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
PIPELINE="$REPO_ROOT/pipeline"

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

TAG=""
OUT="dist"
GH_REPO="${GITHUB_REPOSITORY:-Hamza-Labs-Core/Maktaba}"
while [ $# -gt 0 ]; do
    case "$1" in
        --version) [ $# -ge 2 ] || die "--version needs a value"; TAG="$2"; shift 2 ;;
        --out)     [ $# -ge 2 ] || die "--out needs a value"; OUT="$2"; shift 2 ;;
        --repo)    [ $# -ge 2 ] || die "--repo needs a value"; GH_REPO="$2"; shift 2 ;;
        -h|--help) sed -n '2,/^set -e/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
        *)         die "unknown option '$1'" ;;
    esac
done
[ -n "$TAG" ] || die "--version is required"
command -v uv >/dev/null 2>&1 || die "uv is required (https://docs.astral.sh/uv/)"
# The constraints name the indexes beside PyPI (torch's CUDA build for Windows comes from
# PyTorch's): uv export writes them with --emit-index-url, new in uv 0.12. An older uv
# would write a file without them, which cannot install torch on Windows.
UV_MIN="0.12.0"
uv_version="$(uv --version | awk '{print $2}')"
if [ "$(printf '%s\n%s\n' "$UV_MIN" "$uv_version" | sort -t. -k1,1n -k2,2n -k3,3n | head -n1)" != "$UV_MIN" ]; then
    die "uv $uv_version is too old: the constraints need uv $UV_MIN or later (uv export --emit-index-url). Update it with \`uv self update\` or from https://docs.astral.sh/uv/"
fi
command -v zip >/dev/null 2>&1 || die "zip is required (the Windows archive)"
case "$TAG" in v*) ;; *) TAG="v$TAG" ;; esac
VERSION="${TAG#v}"

sha256() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
    else shasum -a 256 "$1" | awk '{print $1}'; fi
}

mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/maktaba-node-release.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

# --- the wheel: a stamped copy of the pipeline ---------------------------------------------
src="$WORK/pipeline"
mkdir -p "$src"
cp "$PIPELINE/pyproject.toml" "$PIPELINE/uv.lock" "$PIPELINE/README.md" "$PIPELINE/.python-version" "$src/"
cp -R "$PIPELINE/src" "$src/src"
find "$src/src" -name __pycache__ -type d -prune -exec rm -rf {} +
# The version lives in two places: the package metadata and `maktaba-pipeline --version`.
stamp() {
    sed -i.bak -e "s/^version = \".*\"/version = \"$1\"/" "$src/pyproject.toml"
    sed -i.bak -e "s/^__version__ = \".*\"/__version__ = \"$1\"/" "$src/src/maktaba_pipeline/__init__.py"
    rm -f "$src/pyproject.toml.bak" "$src/src/maktaba_pipeline/__init__.py.bak"
    grep -q "^version = \"$1\"" "$src/pyproject.toml" || die "could not stamp the version into pyproject.toml"
    grep -q "^__version__ = \"$1\"" "$src/src/maktaba_pipeline/__init__.py" || die "could not stamp __version__"
}

# build_wheel: the one wheel in $WORK/wheel, by name. Hatchling rejects a version that is
# not PEP 440, so a bad tag fails here.
build_wheel() {
    local wheels
    rm -rf "$WORK/wheel"
    uv build --wheel --project "$src" --out-dir "$WORK/wheel" --quiet
    wheels=("$WORK"/wheel/maktaba_pipeline-*-py3-none-any.whl)
    if [ "${#wheels[@]}" -ne 1 ] || [ ! -f "${wheels[0]}" ]; then
        die "expected one maktaba_pipeline wheel, got: ${wheels[*]}"
    fi
    basename "${wheels[0]}"
}

stamp "$VERSION"
WHEEL="$(build_wheel)"
# The normalised version (1.2.3-rc.1 -> 1.2.3rc1) names every asset; stamp it again so
# `--version` says what the wheel's metadata says.
PEP_VERSION="${WHEEL#maktaba_pipeline-}"
PEP_VERSION="${PEP_VERSION%-py3-none-any.whl}"
if [ "$PEP_VERSION" != "$VERSION" ]; then
    stamp "$PEP_VERSION"
    WHEEL="$(build_wheel)"
fi
cp "$WORK/wheel/$WHEEL" "$OUT/$WHEEL"

# --- the constraints: uv.lock's pins for the runtime and every extra but dev ------------------
CONSTRAINTS="maktaba-pipeline-$PEP_VERSION-constraints.txt"
extras=()
# (a Windows Python prints CRLF: strip the CR, or uv rejects the extra's name)
while IFS=$'\r' read -r e; do extras+=(--extra "$e"); done < <(
    uv run --no-project --quiet python - "$src/pyproject.toml" <<'PY'
import sys, tomllib
with open(sys.argv[1], "rb") as f:
    project = tomllib.load(f)["project"]
for name in project.get("optional-dependencies", {}):
    if name != "dev":
        print(name)
PY
)
# With the hashes uv.lock records: the installers install in hash-checking mode
# (--require-hashes), so a dependency that PyPI serves differently is refused. And with
# the indexes ([[tool.uv.index]]: PyTorch's, for torch's CUDA build on Windows), which the
# installers search beside PyPI; a file that matches no hash of its pin is never taken,
# whichever index serves it.
uv export --project "$src" --frozen --no-dev --no-emit-project --quiet --emit-index-url \
    ${extras[@]+"${extras[@]}"} -o "$WORK/$CONSTRAINTS"
# The export header names this script's temporary path: keep the file reproducible.
grep -v '^#    uv export' "$WORK/$CONSTRAINTS" >"$OUT/$CONSTRAINTS"
# every index of pyproject.toml is in the file, or torch's pin cannot be installed
while IFS=$'\r' read -r url; do
    [ -n "$url" ] || continue
    grep -qxF -- "--extra-index-url $url" "$OUT/$CONSTRAINTS" \
        || die "$CONSTRAINTS does not name the index $url (uv export --emit-index-url)"
done < <(
    uv run --no-project --quiet python - "$src/pyproject.toml" <<'PY'
import sys, tomllib
with open(sys.argv[1], "rb") as f:
    for index in tomllib.load(f).get("tool", {}).get("uv", {}).get("index", []):
        print(index["url"])
PY
)
# every requirement carries a hash (the installers refuse the file otherwise)
unhashed="$(awk '
    /^[A-Za-z0-9]/ { if (req != "" && !h) bad = bad " " req; req = $1; h = ($0 ~ /--hash=sha256:/); next }
    /--hash=sha256:/ { h = 1 }
    END { if (req != "" && !h) bad = bad " " req; print bad }' "$OUT/$CONSTRAINTS")"
[ -z "$unhashed" ] || die "$CONSTRAINTS has requirements without a hash:$unhashed"
if grep -qE '^(-e |file:|\./)' "$OUT/$CONSTRAINTS"; then
    die "$CONSTRAINTS has a local requirement, which a machine without the checkout cannot install"
fi

# --- the installer bundle ------------------------------------------------------------------------
BUNDLE="maktaba-node-$PEP_VERSION"
stage="$WORK/$BUNDLE"
mkdir -p "$stage"
(cd "$SCRIPT_DIR" && tar --exclude=./tests --exclude=./build-release.sh -cf - .) | (cd "$stage" && tar -xf -)
cp "$OUT/$WHEEL" "$OUT/$CONSTRAINTS" "$stage/"
cat >"$stage/release.conf" <<EOF
# Written by build-release.sh: install-node.sh / install-node.ps1 install this release's
# pipeline when given neither --from-wheel nor --source. Not sourced; KEY=VALUE only.
TAG=$TAG
VERSION=$PEP_VERSION
REPO=$GH_REPO
WHEEL=$WHEEL
WHEEL_SHA256=$(sha256 "$OUT/$WHEEL")
CONSTRAINTS=$CONSTRAINTS
CONSTRAINTS_SHA256=$(sha256 "$OUT/$CONSTRAINTS")
PYTHON=$(tr -d '[:space:]' <"$PIPELINE/.python-version")
EOF
chmod 0755 "$stage/install-node.sh" "$stage/uninstall-node.sh" "$stage/launchd/maktaba-node-run"

# Deterministic archives where the tools allow: sorted names, pinned mtime, no owners.
sde="${SOURCE_DATE_EPOCH:-$(git -C "$REPO_ROOT" log -1 --pretty=%ct 2>/dev/null || date +%s)}"
if tar --version 2>/dev/null | grep -q 'GNU tar'; then
    tar --sort=name --mtime="@$sde" --owner=0 --group=0 --numeric-owner \
        -C "$WORK" -czf "$OUT/$BUNDLE.tar.gz" "$BUNDLE"
else
    tar -C "$WORK" -czf "$OUT/$BUNDLE.tar.gz" "$BUNDLE"
fi
(cd "$WORK" && rm -f "$OUT/$BUNDLE.zip" && zip -qrX "$OUT/$BUNDLE.zip" "$BUNDLE")

printf '%s\n' "$OUT/$WHEEL" "$OUT/$CONSTRAINTS" "$OUT/$BUNDLE.tar.gz" "$OUT/$BUNDLE.zip"
