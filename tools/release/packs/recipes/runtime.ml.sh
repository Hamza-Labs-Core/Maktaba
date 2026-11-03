#!/usr/bin/env bash
# runtime.ml: the wheels of the pipeline's stt and search extras for the platform, with
# their constraints (and on Windows the cuda group's too: torch's CUDA build for a machine
# whose NVIDIA GPU runs the second listen).
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cuda=()
[ "$2" != windows-amd64 ] || cuda=(--cuda)
exec bash "$here/../wheelhouse.sh" "$1" "$2" "$3" ${cuda[@]+"${cuda[@]}"} --extra stt --extra search
