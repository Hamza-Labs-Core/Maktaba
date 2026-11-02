#!/usr/bin/env bash
# Checks tools/release/play-version.sh: the versionCode grows with every tag, stays within
# Google Play's limit, and a tag that cannot be one is refused. Needs only bash.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

want() { # TAG NAME CODE
    local out
    if ! out="$(bash "${here}/play-version.sh" "$1" 2>&1)"; then
        fail "$1: refused (${out})"
        return
    fi
    if [ "${out}" = "$(printf 'name=%s\ncode=%s' "$2" "$3")" ]; then
        ok "$1 -> $2 ($3)"
    else
        fail "$1: got ${out//$'\n'/ }, want name=$2 code=$3"
    fi
}

refused() { # TAG
    if bash "${here}/play-version.sh" "$1" >/dev/null 2>&1; then
        fail "'$1': accepted, want refused"
    else
        ok "'$1' refused"
    fi
}

want v0.3.44 0.3.44 3044
want v0.3.45 0.3.45 3045
want v0.4.0 0.4.0 4000
want v0.4.0-rc.1 0.4.0-rc.1 4000
want v1.0.0 1.0.0 1000000
want v1.2.3 1.2.3 1002003
want v0.10.9 0.10.9 10009
want v2100.0.0 2100.0.0 2100000000

refused 0.3.45
refused v0.3
refused v0.3.45.1
refused v0.3.08
refused v0.1000.0
refused v0.3.1000
refused v2100.0.1
refused v99999.0.0
refused v0.0.0
refused 'v1.2.3 '
refused ''

# every tag's code is above the one before it (the order releases are cut in)
prev=0
for t in v0.3.44 v0.3.45 v0.3.999 v0.4.0 v0.999.999 v1.0.0 v1.0.1 v1.1.0 v2.0.0; do
    code="$(bash "${here}/play-version.sh" "${t}" | sed -n 's/^code=//p')"
    if [ "${code}" -gt "${prev}" ]; then
        ok "${t} (${code}) above the tag before"
    else
        fail "${t} (${code}) not above ${prev}"
    fi
    prev="${code}"
done

[ "${fails}" -eq 0 ] || { echo "${fails} failed"; exit 1; }
echo "all passed"
