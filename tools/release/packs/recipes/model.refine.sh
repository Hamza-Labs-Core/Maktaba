#!/usr/bin/env bash
# model.refine: CAMeL Tools' MSA morphology database, laid out as a CAMELTOOLS_DATA folder
# (camel_tools/: data/<destination>/, versions.json naming the package, and the pinned
# catalogue.json, so nothing is fetched when camel_tools.data is imported). The same tree
# models/fetch_cli.py's `dictionary` leaves in <model cache>/camel_tools. Every platform's
# pack holds the same files.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
stage="$1"
# shellcheck source-path=SCRIPTDIR source=../pins.env
. "$here/../pins.env"
# shellcheck source-path=SCRIPTDIR source=../fetch.sh
. "$here/../fetch.sh"

data="$stage/camel_tools"
dest="$data/data/$CAMEL_DESTINATION"
mkdir -p "$dest"
zip="$(fetch "$CAMEL_DB_URL" "$CAMEL_DB_SHA256" "$CAMEL_PACKAGE-$CAMEL_PACKAGE_VERSION.zip")"
unzip -q "$zip" -d "$dest"
check_sha256 "$dest/LICENSE" "$CAMEL_DB_LICENSE_SHA256"
check_sha256 "$dest/morphology.db" "$CAMEL_DB_FILE_SHA256"
# only the two files the catalogue lists
extra="$(find "$dest" -type f ! -name LICENSE ! -name morphology.db)"
[ -z "$extra" ] || { echo "model.refine: unexpected files in the archive: $extra" >&2; exit 1; }
chmod 0644 "$dest/LICENSE" "$dest/morphology.db"
catalogue="$(fetch "$CAMEL_CATALOGUE_URL" "$CAMEL_CATALOGUE_SHA256" catalogue-1.6.json)"
cp "$catalogue" "$data/catalogue.json"
chmod 0644 "$data/catalogue.json"
# camel_tools' own versions file (Catalogue.download_package writes the same)
printf '{\n    "%s": "%s"\n}' "$CAMEL_PACKAGE" "$CAMEL_PACKAGE_VERSION" >"$data/versions.json"
