#!/usr/bin/env bash
# tool.yt-dlp: the wheels of the pipeline's mediaintel extra for the platform.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec bash "$here/../wheelhouse.sh" "$1" "$2" "$3" --extra mediaintel
