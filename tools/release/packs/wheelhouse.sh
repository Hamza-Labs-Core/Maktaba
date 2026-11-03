#!/usr/bin/env bash
# A wheelhouse pack's files (story 51.15): wheels/ (every pinned wheel of the extras for
# the platform, with wheels/index.json in the wheelhouse format the home's mirror serves,
# api/internal/updates/wheelhouse.go WheelIndex) and constraints.txt (uv.lock's pins with
# their hashes, as deploy/packaging/node/build-release.sh writes them).
#
#   wheelhouse.sh STAGE PLATFORM VERSION [--cuda] --extra NAME...
#
# --cuda adds constraints-cuda.txt (the cuda group: torch's CUDA build from PyTorch's
# index) and its wheels. Env: REPO_ROOT, WORK (scratch), SOURCE_DATE_EPOCH, PACKS_PYTHON
# (a Python with pip; default a uv venv of the pipeline's Python).
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$here/../../.." && pwd)}"
stage="$1" platform="$2" version="$3"
shift 3
cuda=0 extras=()
while [ $# -gt 0 ]; do
    case "$1" in
        --cuda) cuda=1; shift ;;
        --extra) extras+=(--extra "$2"); shift 2 ;;
        *) echo "wheelhouse.sh: unknown option $1" >&2; exit 2 ;;
    esac
done
WORK="${WORK:?}"
mkdir -p "$WORK" "$stage/wheels"
pipeline="$REPO_ROOT/pipeline"
pyver="$(tr -d '[:space:]' <"$pipeline/.python-version")"

export_pins() {
    local file="$1"
    shift
    uv export --project "$pipeline" --frozen --no-dev --no-emit-project --quiet --emit-index-url \
        "${extras[@]}" "$@" -o "$WORK/$file.raw"
    # the export header names a temporary path: keep the file reproducible
    grep -v '^#    uv export' "$WORK/$file.raw" >"$stage/$file"
}
export_pins constraints.txt
# PyPI's alone: no pin of the default set comes from another index (build-release.sh)
grep -v '^--extra-index-url ' "$stage/constraints.txt" >"$WORK/c" && mv "$WORK/c" "$stage/constraints.txt"
sets=("$stage/constraints.txt")
if [ "$cuda" = 1 ]; then
    export_pins constraints-cuda.txt --group cuda
    sets+=("$stage/constraints-cuda.txt")
fi

py="${PACKS_PYTHON:-}"
if [ -z "$py" ]; then
    uv venv --quiet --seed --python "$pyver" "$WORK/pipenv"
    py="$WORK/pipenv/bin/python"
fi
"$py" -I "$here/wheelhouse.py" --dest "$stage/wheels" --platform "$platform" --python "$pyver" \
    --version "$version" "${sets[@]}"
