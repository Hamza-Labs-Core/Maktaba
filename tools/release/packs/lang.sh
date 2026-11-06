#!/usr/bin/env bash
# lang.sh STAGE ID: a language data pack (Epic 52, story 52.16): the dictionary files
# lang-sources.toml pins for ID, each downloaded at its commit and checked against its
# sha256 (fetch.sh), plus the pack's NOTICE and samples.txt. Only gathers files: nothing in
# the pack runs. Every platform's pack holds the same bytes.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
stage="$1" id="$2"
REPO_ROOT="${REPO_ROOT:-$(cd "$here/../../.." && pwd)}"
# shellcheck source-path=SCRIPTDIR source=fetch.sh
. "$here/fetch.sh"
sources="${LANG_SOURCES:-$here/lang-sources.toml}"
python3 - "$sources" "$id" >"${WORK:?}/lang-files.tsv" <<'PY'
import sys, tomllib
raw = tomllib.load(open(sys.argv[1], "rb"))
pack = raw.get("pack", {}).get(sys.argv[2])
if not pack:
    sys.exit(f"lang.sh: {sys.argv[2]} is not in {sys.argv[1]}")
for f in pack["files"]:
    print(f"{raw['base']}/{f['path']}\t{f['sha256']}\t{f['name']}")
PY
mkdir -p "$stage"
while IFS=$'\t' read -r url sum name; do
    [ -n "$url" ] || continue
    got="$(fetch "$url" "$sum" "$id-$name")"
    cp "$got" "$stage/$name"
    chmod 0644 "$stage/$name"
done <"$WORK/lang-files.tsv"
code="${id#lang.}"
code="${code#hunspell.}"
packdir="$REPO_ROOT/pipeline/src/maktaba_pipeline/language_packs/$code"
for f in NOTICE samples.txt; do
    if [ -f "$packdir/$f" ]; then
        cp "$packdir/$f" "$stage/$f"
        chmod 0644 "$stage/$f"
    fi
done
if [ ! -f "$stage/NOTICE" ]; then
    licence="$(python3 -c 'import sys,tomllib
for l in tomllib.load(open(sys.argv[1],"rb"))["language"]:
    if l["code"]==sys.argv[2]: print(l["licence"])' "$here/hunspell-languages.toml" "$code")"
    printf 'Hunspell dictionary for %s (Maktaba word review, Epic 52)\n\nFrom the LibreOffice dictionaries repository; licence: %s. The licence and\nreadme files of the dictionary are in this pack beside it.\n' "$code" "$licence" >"$stage/NOTICE"
    chmod 0644 "$stage/NOTICE"
fi
