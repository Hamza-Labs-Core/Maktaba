#!/usr/bin/env bash
# The update rehearsal's pipeline environments (docs/update-rehearsal.md, scenarios 16.x):
# one per release of the kit (tools/rehearsal/build.sh), from the release's own pipeline
# wheel and its tree's runtime pins, with no extra (no ML), plus the rehearsal's sleepy
# stages (tools/rehearsal/sleepy: inert unless a scenario names stages).
#
#   venvs.sh --kit DIR [--out DIR] [--python PYTHON]
#
#   <out>/<version>/   the environment of each release (default out: <kit>/venvs): prev's
#                      and cur's; next and after, built from the same commit as cur, are
#                      links to cur's
#
# An environment already there (its .rehearsal-ok written last) is kept: CI restores each
# from its cache, keyed by the release (the previous release's tag; this commit's pipeline,
# plugins and sleepy stages), so each is built once, not per scenario. The paths must stay
# the same between the build and its use (a venv is not relocatable): CI's KIT is fixed.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
kit="" out="" python="${PYTHON:-python3.12}"
while [ $# -gt 0 ]; do
    case "$1" in
        --kit) kit="$2"; shift 2 ;;
        --out) out="$2"; shift 2 ;;
        --python) python="$2"; shift 2 ;;
        *) echo "venvs.sh: unknown option $1" >&2; exit 2 ;;
    esac
done
[ -n "$kit" ] || { echo "usage: venvs.sh --kit DIR [--out DIR] [--python PYTHON]" >&2; exit 2; }
[ -f "$kit/versions.env" ] || { echo "venvs.sh: $kit/versions.env: no kit there" >&2; exit 1; }
out="${out:-$kit/venvs}"
# shellcheck disable=SC1091 # the kit's, written by build.sh
. "$kit/versions.env"
mkdir -p "$out"

# venv REL VERSION: the environment of the kit's REL/pipeline wheel as <out>/VERSION
venv() {
    local rel="$1" version="$2" dir="$out/$2" whl site
    if [ -f "$dir/.rehearsal-ok" ]; then
        echo "venvs.sh: $version: kept"
        return 0
    fi
    whl=("$kit/$rel"/pipeline/maktaba_pipeline-*-py3-none-any.whl)
    [ -f "${whl[0]}" ] || { echo "venvs.sh: no pipeline wheel in $kit/$rel/pipeline" >&2; exit 1; }
    echo "venvs.sh: $version: $(basename "${whl[0]}")"
    rm -rf "$dir"
    uv venv --quiet --python "$python" "$dir"
    uv pip install --quiet --python "$dir/bin/python" -c "$kit/$rel/pipeline/constraints.txt" "${whl[0]}"
    site="$("$dir/bin/python" -c 'import sysconfig; print(sysconfig.get_paths()["purelib"])')"
    cp "$here/sleepy/maktaba_rehearsal_sleepy.py" "$site/"
    echo "import maktaba_rehearsal_sleepy" >"$site/maktaba_rehearsal_sleepy.pth"
    "$dir/bin/python" -c "import maktaba_pipeline, maktaba_plugin_runner; print('venvs.sh: $version answers', maktaba_pipeline.__version__)" 2>/dev/null ||
        "$dir/bin/python" -c "import maktaba_pipeline; print('venvs.sh: $version answers', maktaba_pipeline.__version__)"
    touch "$dir/.rehearsal-ok"
}

venv prev "$PREV"
venv cur "$CUR"
for v in "$NEXT" "${AFTER:-}"; do
    [ -n "$v" ] || continue
    rm -rf "${out:?}/$v"
    ln -s "$CUR" "$out/$v"
done
echo "venvs.sh: the environments are in $out"
