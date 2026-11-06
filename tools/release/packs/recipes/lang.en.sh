#!/usr/bin/env bash
# lang.en: the Hunspell dictionary of the en language pack (Epic 52, story 52.16),
# pinned in ../lang-sources.toml.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec bash "$here/../lang.sh" "$1" lang.en
