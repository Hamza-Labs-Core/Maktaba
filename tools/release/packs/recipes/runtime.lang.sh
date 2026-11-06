#!/usr/bin/env bash
# runtime.lang: the wheels of the pipeline's lang extra for the platform (Epic 52, story
# 52.16): wordfreq, and PyICU where it has a wheel; the generic Hunspell pack and the
# fallback pack run on them.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec bash "$here/../wheelhouse.sh" "$1" "$2" "$3" --extra lang
