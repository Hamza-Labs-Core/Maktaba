#!/usr/bin/env bash
# lang.hunspell.<code>: one LibreOffice Hunspell dictionary for the generic pack (Epic 52,
# stories 52.3 and 52.16). build.sh runs this recipe for every lang.hunspell.* id of
# hunspell-languages.toml, with the id in PACK_ID.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec bash "$here/../lang.sh" "$1" "${PACK_ID:?the lang.hunspell.<code> id}"
